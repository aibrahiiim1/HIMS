package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/aruba"
	"github.com/coralsearesorts/hims/internal/arubacentral"
	"github.com/coralsearesorts/hims/internal/omada"
	"github.com/coralsearesorts/hims/internal/ruckus"
	"github.com/coralsearesorts/hims/internal/ruckuszd"
	"github.com/coralsearesorts/hims/internal/unifi"
)

// wlCheck is one step of a Test Connection run, so the operator sees EXACTLY
// which part worked or failed (reachability vs auth vs a missing field) instead
// of a single opaque pass/fail.
type wlCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | fail | warn | skip
	Detail string `json:"detail,omitempty"`
}

// wlProbeResult is the structured outcome of a pre-persist Test Connection. It
// never persists anything and never echoes the password.
type wlProbeResult struct {
	Vendor        string    `json:"vendor"`
	OK            bool      `json:"ok"` // overall: authenticated and safe to add + collect
	Reachable     bool      `json:"reachable"`
	Authenticated bool      `json:"authenticated"`
	APIVersion    string    `json:"api_version,omitempty"`
	Detail        string    `json:"detail"`
	Checks        []wlCheck `json:"checks"`
}

// testWirelessController handles POST /wireless/controllers/test — a pre-persist
// connection test. It builds an in-memory profile from the same payload as the
// add form, runs the driver's real login/probe, and returns a structured result.
// Nothing is written to the database and no credential is stored.
func (s *Server) testWirelessController(w http.ResponseWriter, r *http.Request) {
	var req addWirelessControllerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Vendor = strings.TrimSpace(req.Vendor)
	ven, known := wlVendorByKey(req.Vendor)
	if !known || ven.Status == wlStatusUnsupported {
		http.Error(w, "unknown or unsupported wireless vendor: "+req.Vendor, http.StatusBadRequest)
		return
	}

	res := wlProbeResult{Vendor: ven.DisplayName}

	// Field validation is a real check the operator wants to see pass/fail.
	var missing []string
	for _, fld := range ven.Fields {
		if fld.Required && strings.TrimSpace(reqFieldValue(req, fld.Key)) == "" {
			missing = append(missing, fld.Label)
		}
	}
	if len(missing) > 0 {
		res.Checks = append(res.Checks, wlCheck{Name: "Required fields", Status: "fail", Detail: "missing: " + strings.Join(missing, ", ")})
		res.Detail = ven.DisplayName + " needs: " + strings.Join(missing, ", ")
		writeJSON(w, http.StatusOK, res)
		return
	}
	res.Checks = append(res.Checks, wlCheck{Name: "Required fields", Status: "ok"})

	ip, err := netip.ParseAddr(strings.TrimSpace(req.IP))
	if err != nil {
		res.Checks = append(res.Checks, wlCheck{Name: "Controller IP", Status: "fail", Detail: "invalid IP address"})
		res.Detail = "invalid controller IP"
		writeJSON(w, http.StatusOK, res)
		return
	}
	if missing := missingCredential(ven, req); missing != "" {
		res.Checks = append(res.Checks, wlCheck{Name: "Credentials", Status: "fail", Detail: missing})
		res.Detail = missing
		writeJSON(w, http.StatusOK, res)
		return
	}

	// A gated driver (Aruba) is honest: nothing to authenticate against yet.
	if ven.Status != wlStatusWorking {
		res.Checks = append(res.Checks, wlCheck{Name: "Collector", Status: "warn", Detail: ven.NextAction})
		res.Detail = ven.Message
		writeJSON(w, http.StatusOK, res)
		return
	}

	port := req.Port
	if port <= 0 {
		port = ven.DefaultPort
	}
	base := "https://" + ip.String() + ":" + strconv.Itoa(port)

	cfg := vpConfig{SSLVerify: req.SSLVerify}
	for _, fld := range ven.Fields {
		val := strings.TrimSpace(reqFieldValue(req, fld.Key))
		if val == "" {
			val = fld.Default
		}
		switch fld.Key {
		case "site":
			cfg.Site = val
		case "controller_id":
			cfg.ControllerID = val
		case "api_base":
			cfg.APIBase = val
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	s.probeWirelessController(ctx, ven, base, strings.TrimSpace(req.Username), req.Password, cfg, &res)
	writeJSON(w, http.StatusOK, res)
}

// probeWirelessController runs the real per-driver login + a light read-only
// capability probe (an AP query) WITHOUT persisting anything, filling in the
// reachable / authenticated / version checks. Reuses the exact client
// constructors the collectors use so a pre-persist test mirrors collection.
func (s *Server) probeWirelessController(ctx context.Context, ven wlVendor, base, user, pass string, cfg vpConfig, res *wlProbeResult) {
	doer := insecureDoer(20 * time.Second)
	authOK := func(apCount int, version string) {
		res.Reachable, res.Authenticated, res.OK = true, true, true
		res.APIVersion = version
		res.Checks = append(res.Checks, wlCheck{Name: "Reachable", Status: "ok", Detail: base})
		res.Checks = append(res.Checks, wlCheck{Name: "Authentication", Status: "ok", Detail: ven.LoginMethod})
		if version != "" {
			res.Checks = append(res.Checks, wlCheck{Name: "API version / path", Status: "ok", Detail: version})
		}
		res.Checks = append(res.Checks, wlCheck{Name: "AP query", Status: "ok", Detail: itoaN(apCount) + " AP(s) visible"})
		res.Detail = ven.DisplayName + " authenticated — " + itoaN(apCount) + " AP(s) visible."
	}
	failAuth := func(err error) {
		reachable, reason := classifyProbeErr(err)
		res.Reachable = reachable
		if reachable {
			res.Checks = append(res.Checks, wlCheck{Name: "Reachable", Status: "ok", Detail: base})
			res.Checks = append(res.Checks, wlCheck{Name: "Authentication", Status: "fail", Detail: reason})
			res.Detail = ven.DisplayName + " reachable but login failed: " + reason
		} else {
			res.Checks = append(res.Checks, wlCheck{Name: "Reachable", Status: "fail", Detail: reason})
			res.Checks = append(res.Checks, wlCheck{Name: "Authentication", Status: "skip", Detail: "skipped — endpoint not reachable"})
			res.Detail = ven.DisplayName + " not reachable at " + base + ": " + reason
		}
	}

	switch ven.profileVendorType {
	case "ruckus_zd":
		c := ruckuszd.New(base, user, pass, ruckusZDDoer(cfg, 15*time.Second))
		n, err := c.Ping(ctx)
		if err != nil {
			failAuth(err)
			return
		}
		authOK(n, "admin path /"+c.AdminBase()+"/")
	case "extreme_xcc":
		c := s.xccClient(cfg, base, user, pass, 15*time.Second)
		rep := c.Explore(ctx)
		if !rep.Authenticated {
			res.Reachable = rep.SuggestedAPIBase != "" || strings.Contains(strings.ToLower(rep.Summary), "401") || strings.Contains(strings.ToLower(rep.Summary), "403")
			st := "fail"
			if res.Reachable {
				res.Checks = append(res.Checks, wlCheck{Name: "Reachable", Status: "ok", Detail: base})
			} else {
				res.Checks = append(res.Checks, wlCheck{Name: "Reachable", Status: "fail", Detail: rep.Summary})
			}
			res.Checks = append(res.Checks, wlCheck{Name: "Authentication", Status: st, Detail: nz(rep.Summary, "login not confirmed")})
			res.Detail = ven.DisplayName + " — " + rep.Summary
			return
		}
		res.Reachable, res.Authenticated, res.OK = true, true, true
		res.APIVersion = rep.SuggestedAPIBase
		res.Checks = append(res.Checks, wlCheck{Name: "Reachable", Status: "ok", Detail: base})
		res.Checks = append(res.Checks, wlCheck{Name: "Authentication", Status: "ok", Detail: ven.LoginMethod})
		if rep.SuggestedAPIBase != "" {
			res.Checks = append(res.Checks, wlCheck{Name: "API base", Status: "ok", Detail: rep.SuggestedAPIBase})
		}
		res.Detail = ven.DisplayName + " authenticated — " + rep.Summary
	case "wireless_unifi":
		c := unifi.NewClient(base, nz(cfg.Site, "default"), user, pass, doer)
		if err := c.Login(ctx); err != nil {
			failAuth(err)
			return
		}
		aps, _ := c.ListAPs(ctx)
		authOK(len(aps), "site "+nz(cfg.Site, "default"))
	case "wireless_omada":
		c := omada.NewClient(base, cfg.ControllerID, nz(cfg.Site, "Default"), user, pass, doer)
		if err := c.Login(ctx); err != nil {
			failAuth(err)
			return
		}
		aps, _ := c.ListAPs(ctx)
		authOK(len(aps), "site "+nz(cfg.Site, "Default"))
	case "wireless_ruckus":
		c := ruckus.NewClient(base, cfg.APIBase, user, pass, doer)
		if err := c.Login(ctx); err != nil {
			failAuth(err)
			return
		}
		aps, _ := c.ListAPs(ctx)
		authOK(len(aps), nz(cfg.APIBase, "/wsg/api/public"))
	case "wireless_aruba", "wireless_aruba_os10":
		c := aruba.NewClient(base, user, pass, doer)
		if err := c.Login(ctx); err != nil {
			failAuth(err)
			return
		}
		aps, _ := c.ListAPs(ctx)
		authOK(len(aps), "ArubaOS 8-compatible showcommand")
	case "wireless_aruba_central":
		token := pass
		if token == "" {
			token = user
		}
		c := arubacentral.NewClient(centralGateway(cfg.APIBase, base), token, doer)
		n, err := c.Ping(ctx)
		if err != nil {
			failAuth(err)
			return
		}
		authOK(n, "Aruba Central OAuth2 bearer")
	default:
		res.Checks = append(res.Checks, wlCheck{Name: "Collector", Status: "warn", Detail: "no connection test for this driver"})
		res.Detail = "No connection test implemented for " + ven.DisplayName + "."
	}
}

// classifyProbeErr splits a login/probe error into "endpoint unreachable" vs
// "reached but rejected", so the operator gets the exact failure class. The
// reason is a short, non-secret message.
func classifyProbeErr(err error) (reachable bool, reason string) {
	reason = shortErr(err)
	low := strings.ToLower(reason)
	for _, s := range []string{"connection refused", "timeout", "timed out", "no such host", "deadline exceeded", "no route", "i/o timeout", "dial tcp", "eof", "connection reset", "handshake"} {
		if strings.Contains(low, s) {
			return false, reason
		}
	}
	return true, reason
}
