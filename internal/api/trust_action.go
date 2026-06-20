package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/collect"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Guided Trust Actions — safe, operator-triggered remediation for actionable trust gaps. Each
// action maps a trust pattern to the PROVEN collector and returns an honest, normalised result
// (collected | queued | failed | needs_credential | unsupported). Safety: operator-triggered, the
// UI confirms first, every run is guarded against concurrent duplicates (no retry storm), and the
// underlying collectors only classify/bind on REAL success — there is no fabricated "fixed".

type trustActResult struct {
	Action     string `json:"action"`
	Status     string `json:"status"` // collected | queued | failed | needs_credential | unsupported
	Detail     string `json:"detail"`
	Credential string `json:"credential_used,omitempty"`
	At         string `json:"at"`
}

var (
	taMu       sync.Mutex
	taInflight = map[string]bool{}
	taLast     = map[string]trustActResult{} // keyed by device_id (last guided action result)
)

func taStart(key string) bool {
	taMu.Lock()
	defer taMu.Unlock()
	if taInflight[key] {
		return false
	}
	taInflight[key] = true
	return true
}
func taEnd(key string) { taMu.Lock(); delete(taInflight, key); taMu.Unlock() }
func taRecord(devID string, res trustActResult) {
	taMu.Lock()
	taLast[devID] = res
	taMu.Unlock()
}

// lastTrustAction returns the last guided-action result for a device (for the UI's last-attempt /
// last-result columns), or false if none.
func lastTrustAction(devID string) (trustActResult, bool) {
	taMu.Lock()
	defer taMu.Unlock()
	r, ok := taLast[devID]
	return r, ok
}

// trustAction (POST /discovery/trust-action) runs ONE guided action for ONE device.
func (s *Server) trustAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		Action   string `json:"action"` // onboard_esxi | collect_bmc | retry_ssh | rerun_windows
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(req.DeviceID)
	if err != nil {
		http.Error(w, "invalid device_id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		writeJSON(w, http.StatusOK, taFinish(req.DeviceID, trustActResult{Action: req.Action, Status: "unsupported", Detail: "device has no IP address"}))
		return
	}

	// Concurrency guard — no duplicate run of the same action on the same device.
	key := id.String() + ":" + req.Action
	if !taStart(key) {
		http.Error(w, "this action is already running for this device", http.StatusConflict)
		return
	}
	defer taEnd(key)

	// Deep collection can run for minutes; detach from the request lifecycle but keep it bounded.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()

	ip := dev.PrimaryIp.String()
	var res trustActResult
	res.Action = req.Action

	switch req.Action {
	case "onboard_esxi":
		if !probeHost(ip).esxi { // guard: only when the vSphere SDK actually answers
			res.Status, res.Detail = "unsupported", "no vSphere SDK (vim25) detected on this host"
			break
		}
		vr := s.runVSphereCollection(cctx, dev) // classifies virtual_host + binds ONLY on success
		switch {
		case vr.ok():
			res.Status = "collected"
			res.Credential = vr.CredentialUsed
			res.Detail = vr.Detail
		case isAuthReason(vr.Reason):
			res.Status, res.Detail = "needs_credential", nz(vr.Detail, "no VMware credential authenticated — add/select an ESXi root credential")
		default:
			res.Status, res.Detail = "failed", nz(vr.Detail, vr.Reason)
		}

	case "collect_bmc":
		if !probeHost(ip).redfish { // guard: only when a Redfish endpoint answers
			res.Status, res.Detail = "unsupported", "no Redfish/BMC endpoint detected on this host"
			break
		}
		if s.reg == nil || s.fetcher == nil || s.cipher() == nil {
			res.Status, res.Detail = "failed", "collection not configured (needs DB + encryption key)"
			break
		}
		out, cerr := collect.Controller(cctx, s.collectDeps(cctx), "redfish", *dev.PrimaryIp, dev.LocationID, collect.ControllerOpts{})
		switch {
		case cerr == nil:
			res.Status, res.Detail = "collected", nz(out.Summary, "BMC collected via Redfish")
		case isAuthErr(cerr.Error()):
			res.Status, res.Detail = "needs_credential", "needs a correct iLO/iDRAC (http_basic) credential"
		default:
			res.Status, res.Detail = "failed", "Redfish collection failed: "+shortErr(cerr)
		}

	case "retry_ssh":
		// Force the SSH collector even on a host whose OS is not yet classified (port 22 / SSH
		// evidence exists). On a successful SSH login it persists Linux inventory, binds the
		// credential, and auto-classifies Linux from the OS caption — classification changes ONLY
		// on real success.
		res = s.guidedSSHCollect(cctx, dev)

	case "rerun_windows":
		// Full Windows ladder via runOSCollection: WinRM/PSRP → WMI/DCOM → WSMan/CIM → relay agent.
		// Writes os_inventory + binds ONLY on success.
		or := s.runOSCollection(cctx, dev)
		switch {
		case or.queued():
			res.Status = "queued"
			res.Detail = nz(or.Detail, "dispatched to the site relay agent — completes asynchronously")
		case or.ok():
			res.Status = "collected"
			res.Credential = or.CredentialUsed
			res.Detail = nz(or.Detail, "collected via "+or.Method)
		case isAuthReason(or.Reason):
			res.Status, res.Detail = "needs_credential", nz(or.Detail, "no credential authenticated")
		default:
			res.Status, res.Detail = "failed", nz(or.Detail, or.Reason)
		}

	default:
		http.Error(w, "unknown action (onboard_esxi|collect_bmc|retry_ssh|rerun_windows)", http.StatusBadRequest)
		return
	}

	s.audit(r, "inventory", "trust.action", "device", id.String(),
		"Guided trust action "+req.Action+" for "+dev.Name+" → "+res.Status, map[string]any{"action": req.Action, "status": res.Status})
	writeJSON(w, http.StatusOK, taFinish(req.DeviceID, res))
}

// taFinish stamps + records the result (best-effort; the stamp is informational).
func taFinish(devID string, res trustActResult) trustActResult {
	res.At = time.Now().UTC().Format(time.RFC3339)
	taRecord(devID, res)
	return res
}

// guidedSSHCollect forces the SSH/Linux collector against a host (even one whose OS is not yet
// classified) with all applicable SSH credentials. On the first successful login it persists the
// Linux inventory, binds the winning credential, marks the host up, and auto-classifies Linux from
// the OS caption — classification + bind happen ONLY on real success (no fake "fixed").
func (s *Server) guidedSSHCollect(ctx context.Context, d db.Device) trustActResult {
	res := trustActResult{Action: "retry_ssh"}
	cph := s.cipher()
	if cph == nil {
		res.Status, res.Detail = "failed", "encryption key not loaded; cannot decrypt credentials"
		return res
	}
	ip := d.PrimaryIp.String()
	cands := s.osCandidateCreds(ctx, cph, d, "ssh")
	if len(cands) == 0 {
		res.Status, res.Detail = "needs_credential", "no usable SSH credential — add one (Administration → Credentials)"
		return res
	}
	for _, cd := range cands {
		rep, err := s.collectWithCred(ctx, "ssh", ip, cd.user, cd.pass)
		if err != nil {
			continue
		}
		if perr := osinv.Persist(ctx, s.queries, d.ID, rep, time.Now().UTC()); perr != nil {
			res.Status, res.Detail = "failed", "collected but failed to save: "+perr.Error()
			return res
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: d.ID, Vendor: rep.Hardware.Manufacturer, Model: rep.Hardware.Model,
			Serial: rep.Hardware.Serial, OsVersion: rep.OS.Caption, Hostname: rep.Identity.Hostname,
		})
		cid := cd.id
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: &cid})
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})
		s.reclassifyFromCaption(ctx, d, rep.OS.Caption)
		res.Status, res.Credential = "collected", cd.name
		res.Detail = "collected via SSH using credential " + cd.name
		return res
	}
	res.Status, res.Detail = "needs_credential", "SSH reachable but no configured credential authenticated"
	return res
}

func isAuthReason(reason string) bool {
	switch reason {
	case "auth_failed", "no_credential", "credential_failed":
		return true
	}
	return strings.Contains(reason, "credential") || strings.Contains(reason, "auth")
}
func isAuthErr(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "no ht") || strings.Contains(s, "credential") || strings.Contains(s, "auth") ||
		strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "no usable")
}
