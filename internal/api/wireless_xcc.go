package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/extremexcc"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

const xccSource = "extreme_xcc_api"

// isExtremeXCC reports whether a scanned host is an on-prem ExtremeCloud IQ
// Controller (VE6120): the VE6120 product sysObjectID, or "ExtremeCloud IQ
// Controller" in the sysDescr. Used to give XCC controllers a specific
// "configure the XCC profile" next action.
func isExtremeXCC(r discovery.HostResult) bool {
	oid := strings.TrimPrefix(strings.TrimSpace(r.Probe.SNMPSysObjectID), ".")
	if oid == "1.3.6.1.4.1.1916.2.284" {
		return true
	}
	return strings.Contains(strings.ToLower(r.Probe.SNMPSysDescr), "extremecloud iq controller")
}

// xccDoer builds the HTTP client for an XCC profile — TLS-insecure by default
// (mgmt-LAN self-signed certs), verifying when the profile opts in.
func xccDoer(cfg vpConfig, timeout time.Duration) *http.Client {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !cfg.SSLVerify, MinVersion: tls.VersionTLS10}} //nolint:gosec
	return &http.Client{Timeout: timeout, Transport: tr}
}

// xccClient builds an extremexcc client from a profile + resolved secret. A
// credential with an empty username is treated as a bare API token.
func (s *Server) xccClient(cfg vpConfig, base, user, pass string, timeout time.Duration) *extremexcc.Client {
	token := ""
	if user == "" && pass != "" {
		token, pass = pass, ""
	}
	return extremexcc.NewClient(base, cfg.APIBase, user, pass, token, xccDoer(cfg, timeout))
}

// exploreXCC runs the safe discovery + saves the suggested API base into the
// profile config. Returns a non-secret, operator-readable summary as the detail.
func (s *Server) exploreXCC(ctx context.Context, p db.VendorConnectionProfile, cfg vpConfig, base, user, pass string) (bool, string) {
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	c := s.xccClient(cfg, base, user, pass, 12*time.Second)
	rep := c.Explore(cctx)

	// Persist the discovered base path so a later Run Collection uses it.
	if rep.SuggestedAPIBase != "" && rep.SuggestedAPIBase != cfg.APIBase {
		cfg.APIBase = rep.SuggestedAPIBase
		if blob, err := json.Marshal(cfg); err == nil {
			_, _ = s.queries.UpdateVendorProfile(ctx, db.UpdateVendorProfileParams{
				ID: p.ID, Name: p.Name, VendorType: p.VendorType, TargetUrl: p.TargetUrl,
				CredentialID: p.CredentialID, LocationID: p.LocationID, DeviceID: p.DeviceID,
				Config: blob, Enabled: p.Enabled,
			})
		}
	}

	// Build a compact, safe probe summary (status + content-type per path) — never
	// bodies or secrets. Highlights the JSON-bearing endpoints.
	var hits []string
	for _, pr := range rep.Probes {
		if pr.JSON || pr.Status == 200 {
			ct := pr.ContentType
			if i := strings.IndexByte(ct, ';'); i >= 0 {
				ct = ct[:i]
			}
			hits = append(hits, pr.Path+"→"+strconv.Itoa(pr.Status)+"("+ct+")")
			if len(hits) >= 6 {
				break
			}
		}
	}
	detail := rep.Summary
	if rep.SuggestedAPIBase != "" {
		detail += " [saved api_base=" + rep.SuggestedAPIBase + "]"
	}
	if len(hits) > 0 {
		detail += " — probed: " + strings.Join(hits, ", ")
	}
	// "ok" means the diagnostic produced an actionable, authenticated surface.
	return rep.Authenticated && rep.SuggestedAPIBase != "", detail
}

// collectXCCProfile runs the deep collection and persists the rosters with
// source=extreme_xcc_api + the profile id. Honest partial detail; never fabricated.
func (s *Server) collectXCCProfile(ctx context.Context, p db.VendorConnectionProfile, dev db.Device) (bool, string) {
	user, pass, hasCred := s.vendorProfileSecret(ctx, p)
	cfg := parseVPConfig(p.Config)
	if !hasCred && user == "" {
		return false, "no usable credential bound to this profile (add admin/API credentials)"
	}
	base := strings.TrimRight(p.TargetUrl, "/")
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	c := s.xccClient(cfg, base, user, pass, 20*time.Second)

	res, err := c.Collect(cctx)
	if err != nil {
		return false, "XCC API authentication failed: " + shortErr(err) + " — Test Connection to (re)discover the API path/auth."
	}

	poll := time.Now().UTC()
	pid := p.ID
	apCount := int32(len(res.APs))
	clientCount := int32(len(res.Stations))
	ssidCount := int32(len(res.SSIDs))
	ven := "Extreme Networks"
	verPtr := nzPtr(res.Version)
	_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
		DeviceID: dev.ID, Vendor: &ven, Version: verPtr, ApCount: apCount, ClientCount: clientCount,
		Source: xccSource, ProfileID: &pid, ControllerName: dev.Name, Model: derefStr(dev.Model),
		Serial: res.Serial, SsidCount: ssidCount,
	})
	for _, a := range res.APs {
		name := a.Name
		if name == "" {
			name = a.MAC
		}
		if name == "" {
			continue // can't uniquely key an AP with no name or MAC
		}
		var ip *netip.Addr
		if addr, perr := netip.ParseAddr(a.IP); perr == nil {
			ip = &addr
		}
		_, _ = s.queries.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
			ControllerDeviceID: dev.ID, Name: name, Mac: nzPtr(a.MAC), Model: nzPtr(a.Model),
			Ip: ip, Status: nz(a.Status, "unknown"), ClientCount: a.ClientCount,
			Serial: a.Serial, Firmware: a.Firmware, Source: xccSource,
		})
	}
	for _, ss := range res.SSIDs {
		if ss.Name == "" {
			continue
		}
		_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
			ControllerDeviceID: dev.ID, Name: ss.Name, Status: nz(ss.Status, "unknown"),
			Security: ss.Security, Band: ss.Band, Vlan: ss.VLAN, ClientCount: ss.ClientCount, Source: xccSource,
		})
	}
	for _, st := range res.Stations {
		if st.MAC == "" {
			continue
		}
		_, _ = s.queries.UpsertWirelessClient(ctx, db.UpsertWirelessClientParams{
			ControllerDeviceID: dev.ID, Mac: st.MAC, Ip: st.IP, Hostname: st.Hostname,
			ApName: st.APName, Ssid: st.SSID, Rssi: st.RSSI, Band: st.Band, Source: xccSource,
		})
	}
	// Prune rows from this source not refreshed this run.
	_ = s.queries.DeleteStaleAccessPoints(ctx, db.DeleteStaleAccessPointsParams{ControllerDeviceID: dev.ID, Source: xccSource, CollectedAt: poll})
	_ = s.queries.DeleteStaleWirelessSSIDs(ctx, db.DeleteStaleWirelessSSIDsParams{ControllerDeviceID: dev.ID, Source: xccSource, CollectedAt: poll})
	_ = s.queries.DeleteStaleWirelessClients(ctx, db.DeleteStaleWirelessClientsParams{ControllerDeviceID: dev.ID, Source: xccSource, CollectedAt: poll})

	if p.CredentialID != nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: dev.ID, CredentialID: p.CredentialID})
	}

	// Honest detail: report exactly which rosters the API exposed.
	parts := []string{strconv.Itoa(len(res.APs)) + " AP(s)", strconv.Itoa(len(res.SSIDs)) + " SSID(s)", strconv.Itoa(len(res.Stations)) + " client(s)"}
	var missing []string
	for _, e := range res.Endpoints {
		if e.Status != 200 {
			missing = append(missing, e.Kind+" ("+nz(e.Note, "unavailable")+")")
		}
	}
	detail := "Extreme XCC collected — " + strings.Join(parts, ", ")
	if len(missing) > 0 {
		detail += "; not exposed: " + strings.Join(missing, ", ")
	}
	// Success when at least one roster came back; otherwise it authenticated but
	// the firmware/API exposed nothing — still honest, but flagged as partial.
	ok := len(res.APs) > 0 || len(res.SSIDs) > 0 || len(res.Stations) > 0
	if !ok {
		detail = "Extreme XCC authenticated but the API exposed no AP/SSID/client roster on this firmware/path. " + detail
	}
	return ok, detail
}

func nzPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
