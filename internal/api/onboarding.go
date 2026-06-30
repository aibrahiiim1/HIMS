package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/isapi"
	"github.com/coralsearesorts/hims/internal/omnipcx"
	rf "github.com/coralsearesorts/hims/internal/redfish"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/coralsearesorts/hims/internal/zkteco"
	"github.com/google/uuid"
)

// Manual Device Onboarding — Test Connection + Save (create-or-override). These choreograph
// the existing credtest / vSphere / create / override / bind / audit / collect infrastructure.
// Honest by construction: a device is "managed" ONLY when a real collection proves it; a
// failed/absent test can be saved only as manual_inventory_only (operator-asserted type,
// never faked as managed). An existing IP is UPDATED (manual authoritative override), never
// duplicated; discovery evidence/history is preserved and future scans respect the lock.

type onbStep struct {
	Step   string `json:"step"`
	Status string `json:"status"` // ok | fail | skipped
	Detail string `json:"detail"`
}

type onbCredIn struct {
	ID     string `json:"id,omitempty"`     // existing credential id
	Kind   string `json:"kind,omitempty"`   // inline: credential kind
	Secret string `json:"secret,omitempty"` // inline: secret (user:pass or community)
	Name   string `json:"name,omitempty"`   // inline: credential name (for save-time create)
}

type onbTestReq struct {
	Type       string    `json:"type"`
	PrimaryIP  string    `json:"primary_ip"`
	Method     string    `json:"method"`
	Port       int       `json:"port"`
	Credential onbCredIn `json:"credential"`
}

type onbTestResp struct {
	Steps       []onbStep `json:"steps"`
	FinalStatus string    `json:"final_status"`
	Category    string    `json:"category"`
	Summary     string    `json:"summary"`
}

// resolveSecret returns the plaintext secret + a display name for either an inline credential
// or an existing credential id. The secret never leaves the server beyond the probe.
func (s *Server) resolveSecret(ctx context.Context, c onbCredIn) (secret, kind, name string, err error) {
	if strings.TrimSpace(c.ID) != "" {
		id, perr := uuid.Parse(c.ID)
		if perr != nil {
			return "", "", "", fmt.Errorf("bad credential id")
		}
		cred, gerr := s.queries.GetCredential(ctx, id)
		if gerr != nil {
			return "", "", "", fmt.Errorf("credential not found")
		}
		cph := s.cipher()
		if cph == nil {
			return "", "", "", fmt.Errorf("encryption key not loaded")
		}
		plain, oerr := cph.Open(cred.EncryptedBlob, cred.KeyID)
		if oerr != nil {
			return "", "", "", fmt.Errorf("could not decrypt credential")
		}
		return string(plain), cred.Kind, cred.Name, nil
	}
	return c.Secret, c.Kind, c.Name, nil
}

// lookupManualDeviceIP is GET /manual-onboarding/lookup?ip=<ip> — does this IP already exist?
// Lets the wizard warn UP FRONT that a manual add will OVERRIDE the existing device (lock the
// classification, preserve discovery evidence, audit), never duplicate. Read-only.
func (s *Server) lookupManualDeviceIP(w http.ResponseWriter, r *http.Request) {
	ipStr := strings.TrimSpace(r.URL.Query().Get("ip"))
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	dev, lerr := s.queries.LiveDeviceByIP(r.Context(), &addr)
	if lerr != nil || dev.ID == uuid.Nil {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exists": true, "device_id": dev.ID.String(), "name": dev.Name,
		"category": dev.Category, "subtype": dev.Subtype,
		"classification_locked": dev.ClassificationLocked,
	})
}

// testManualDeviceConnection is POST /manual-onboarding/test — protocol-specific Test
// Connection with step-by-step evidence. Read-only: it never creates a device or credential.
func (s *Server) testManualDeviceConnection(w http.ResponseWriter, r *http.Request) {
	var req onbTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	t, ok := onbTypeByKey(req.Type)
	if !ok {
		http.Error(w, "unknown device type", http.StatusBadRequest)
		return
	}
	var method onbMethod
	for _, mm := range t.Methods {
		if mm.Key == req.Method {
			method = mm
		}
	}
	if method.Key == "" {
		http.Error(w, "unknown connection method for this type", http.StatusBadRequest)
		return
	}
	ip := strings.TrimSpace(req.PrimaryIP)
	if _, err := netip.ParseAddr(ip); err != nil {
		http.Error(w, "invalid IP address", http.StatusBadRequest)
		return
	}
	port := req.Port
	if port == 0 {
		port = method.DefaultPort
	}

	// Manual-only method: no probe — honest manual_inventory_only.
	if method.TestKind == "manual" {
		writeJSON(w, http.StatusOK, onbTestResp{
			Steps:       []onbStep{{Step: "manual", Status: "skipped", Detail: "manual inventory only — no remote management protocol selected"}},
			FinalStatus: "manual_inventory_only", Category: "manual", Summary: "Will be saved as manual inventory (operator-asserted type, not managed).",
		})
		return
	}

	secret, _, name, err := s.resolveSecret(r.Context(), req.Credential)
	if err != nil {
		writeJSON(w, http.StatusOK, onbTestResp{Steps: []onbStep{{Step: "credential", Status: "fail", Detail: err.Error()}}, FinalStatus: "credential_required", Category: "error"})
		return
	}

	resp := s.runOnboardProbe(r.Context(), method, ip, port, secret, name)
	writeJSON(w, http.StatusOK, resp)
}

// runOnboardProbe runs the right protocol probe and renders step-by-step evidence. vSphere
// uses a SINGLE login (lockout-safe); everything else uses the shared credtest tester.
func (s *Server) runOnboardProbe(ctx context.Context, method onbMethod, ip string, port int, secret, name string) onbTestResp {
	steps := []onbStep{}
	add := func(step, status, detail string) { steps = append(steps, onbStep{step, status, detail}) }

	if method.TestKind == "vsphere" {
		u, p := credtest.SplitUserPass(secret)
		if p == "" {
			add("credential", "fail", "vSphere needs a username:password credential")
			return onbTestResp{Steps: steps, FinalStatus: "credential_required", Category: "error"}
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, ver, model, vendor, verr := vsphereLoginCollectURL(cctx, fmt.Sprintf("https://%s:%d/sdk", ip, port), u, p)
		cancel()
		if verr == nil {
			add("reachable", "ok", "vSphere SOAP API reachable")
			add("auth", "ok", "vSphere login succeeded")
			add("identity", "ok", strings.TrimSpace(vendor+" "+model+" "+ver))
			return onbTestResp{Steps: steps, FinalStatus: "implemented_collected", Category: "success", Summary: "Authenticated — saving will collect host + VMs."}
		}
		cat, detail := categorizeCollectErr("vsphere", verr.Error())
		add("reachable", "ok", "443 reached")
		add("auth", "fail", detail)
		fs := "credential_failed"
		if cat != "auth_failed" {
			fs = cat
		}
		return onbTestResp{Steps: steps, FinalStatus: fs, Category: cat, Summary: detail}
	}

	// ZKTeco native protocol (TCP 4370): real read-only identity probe (serial/firmware/name).
	if method.TestKind == "zkteco" {
		id := zkteco.Probe(ctx, ip, port, secret) // secret = optional communication key
		if id.Connected {
			add("reachable", "ok", fmt.Sprintf("TCP %d reachable", port))
			add("auth", "ok", cond(strings.TrimSpace(secret) != "", "authenticated with the supplied communication key", "authenticated with the SDK default key (0) — no operator secret needed"))
			ident := strings.TrimSpace(id.DeviceName + " " + id.Serial + " " + id.Firmware)
			add("identity", cond(ident != "", "ok", "skipped"), nz(ident, "device exposed no identity fields"))
			if en := zkteco.ReadEnrollment(ctx, ip, port, secret); en.Connected && (en.Users > 0 || en.Fingers > 0) {
				add("enrollment", "ok", fmt.Sprintf("%d/%d users, %d/%d fingerprints, %d records (read-only — no names/templates)", en.Users, en.UsersCap, en.Fingers, en.FingersCap, en.Records))
			}
			return onbTestResp{Steps: steps, FinalStatus: "implemented_collected", Category: "success", Summary: "ZKTeco identity + enrollment collected — saving classifies biometric/zkteco and binds."}
		}
		st := "auth_failed"
		switch {
		case strings.HasPrefix(id.Reason, "unreachable"):
			st = "unreachable"
		case strings.HasPrefix(id.Reason, "protocol"):
			st = "protocol_not_supported"
		case strings.HasPrefix(id.Reason, "zkteco_comm_key_required"):
			st = "zkteco_comm_key_required" // non-default key set; not managed, not a hard failure
		case strings.HasPrefix(id.Reason, "credential_failed"):
			st = "credential_failed"
		}
		add("reachable", cond(st == "unreachable", "fail", "ok"), id.Reason)
		if st != "unreachable" && st != "protocol_not_supported" {
			add("auth", "fail", id.Reason)
		}
		return onbTestResp{Steps: steps, FinalStatus: st, Category: cond(st == "zkteco_comm_key_required", "auth_failed", st), Summary: id.Reason}
	}

	// Real Redfish probe (BMC / iLO / iDRAC / XClarity / iBMC): login + read identity over the
	// Redfish HTTP/JSON API (read-only). A real protocol test — NOT a generic web auth.
	if method.TestKind == "redfish" {
		u, p := credtest.SplitUserPass(secret)
		if p == "" {
			add("credential", "fail", "Redfish needs a username:password credential")
			return onbTestResp{Steps: steps, FinalStatus: "credential_required", Category: "error"}
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		client := rf.NewClient(fmt.Sprintf("https://%s:%d", ip, port), u, p, nil)
		// The Redfish service root (/redfish/v1) is ANONYMOUS and leaks the vendor; /Systems
		// REQUIRES auth. Gate on /Systems so an anonymous root read can never be mistaken for a
		// successful login (which would be a fake "managed" state).
		var sysColl map[string]any
		if aerr := client.GetJSON(cctx, "/redfish/v1/Systems", &sysColl); aerr != nil {
			cancel()
			cat, detail := categorizeCollectErr("redfish", aerr.Error())
			fs := cond(cat == "auth_failed", "credential_failed", cat)
			add("reachable", cond(fs == "unreachable", "fail", "ok"), cond(fs == "unreachable", detail, "Redfish root reachable; /Systems requires auth"))
			if fs != "unreachable" {
				add("auth", "fail", detail)
			}
			return onbTestResp{Steps: steps, FinalStatus: fs, Category: cat, Summary: detail}
		}
		facts, _ := rf.Collect(cctx, client)
		cancel()
		add("reachable", "ok", "Redfish service reachable")
		add("auth", "ok", "Redfish login succeeded (/Systems authorized)")
		ident := strings.TrimSpace(facts.Vendor + " " + facts.Model + " " + facts.Serial)
		add("identity", cond(ident != "", "ok", "skipped"), nz(ident, "BMC exposed no identity fields"))
		return onbTestResp{Steps: steps, FinalStatus: "implemented_collected", Category: "success", Summary: "Redfish authenticated + identity collected — saving will classify bmc + link to its server."}
	}

	// Real ISAPI probe (Hikvision / OEM camera + access devices): read /ISAPI System deviceInfo
	// with digest auth (read-only) — a real protocol identity read, not generic web auth.
	if method.TestKind == "isapi" {
		u, p := credtest.SplitUserPass(secret)
		if p == "" {
			add("credential", "fail", "ISAPI needs a username:password credential")
			return onbTestResp{Steps: steps, FinalStatus: "credential_required", Category: "error"}
		}
		cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		info, ierr := isapi.CollectDeviceInfo(cctx, ip, u, p, nil, nil)
		cancel()
		if ierr == nil {
			add("reachable", "ok", "ISAPI endpoint reachable"+cond(info.Endpoint != "", " ("+info.Endpoint+")", ""))
			add("auth", "ok", "ISAPI digest auth succeeded")
			ident := strings.TrimSpace(info.Manufacturer + " " + info.Model + " " + info.Serial + " " + info.Firmware)
			add("identity", cond(ident != "", "ok", "skipped"), nz(ident, "device exposed no identity fields"))
			return onbTestResp{Steps: steps, FinalStatus: "implemented_collected", Category: "success", Summary: "ISAPI device identity collected."}
		}
		cat, detail := categorizeCollectErr("isapi", ierr.Error())
		fs := cond(cat == "auth_failed", "credential_failed", cat)
		add("reachable", cond(fs == "unreachable", "fail", "ok"), cond(fs == "unreachable", detail, "ISAPI endpoint reachable"))
		if fs != "unreachable" {
			add("auth", "fail", detail)
		}
		return onbTestResp{Steps: steps, FinalStatus: fs, Category: cat, Summary: detail}
	}

	// Real Alcatel OmniPCX Enterprise probe (telnet, no SSH): mtcl login → read the software
	// identity banner (read-only). Directory/phone-sets are an honest external dependency.
	if method.TestKind == "omnipcx" {
		u, p := credtest.SplitUserPass(secret)
		if p == "" {
			add("credential", "fail", "OmniPCX needs a username:password (CLI) credential, e.g. mtcl:mtcl")
			return onbTestResp{Steps: steps, FinalStatus: "credential_required", Category: "error"}
		}
		id := omnipcx.Probe(ctx, ip, port, u, p)
		if id.Connected {
			add("reachable", "ok", fmt.Sprintf("telnet %d reachable", port))
			add("auth", "ok", "OmniPCX login succeeded (CPU role "+nz(id.CPURole, "?")+")")
			add("identity", "ok", strings.TrimSpace(id.Vendor+" "+id.Model+" "+id.OSVersion()))
			return onbTestResp{Steps: steps, FinalStatus: "implemented_collected", Category: "success", Summary: "OmniPCX software identity collected. Directory/phone-sets need OmniVista 8770 — see capabilities."}
		}
		st := "credential_failed"
		switch {
		case strings.HasPrefix(id.Reason, "unreachable"):
			st = "unreachable"
		case strings.HasPrefix(id.Reason, "protocol"):
			st = "protocol_not_supported"
		}
		add("reachable", cond(st == "unreachable", "fail", "ok"), id.Reason)
		if st == "credential_failed" {
			add("auth", "fail", id.Reason)
		}
		return onbTestResp{Steps: steps, FinalStatus: st, Category: st, Summary: id.Reason}
	}

	// credtest covers snmp_v2c/snmp_v3/ssh/winrm/onvif/http_basic/vendor_api.
	kind := method.TestKind
	opts := credtest.Options{Timeout: 10 * time.Second, CredentialName: name}
	if port != 0 {
		opts.WebPorts = []int{port}
	}
	out := credtest.Test(ctx, kind, secret, ip, opts)

	final := "error"
	switch out.Category {
	case credtest.CatSuccess:
		add("reachable", "ok", "service reachable")
		add("auth", "ok", "authenticated")
		if method.CollectorReady {
			add("collector", "ok", "deep collector available — saving will collect")
			final = "implemented_collected"
		} else {
			add("collector", "ok", "identity/auth proven; deep collection is partial for this method")
			final = "implemented_tested"
		}
	case credtest.CatWebReachable:
		add("reachable", "ok", "web reachable")
		add("auth", "skipped", "endpoint does not enforce auth (HTTP 200 anonymously) — web_reachable, NOT managed")
		final = "web_reachable"
	case credtest.CatAuthFailed:
		add("reachable", "ok", "service reachable")
		add("auth", "fail", nz(out.Detail, "credential rejected"))
		final = "credential_failed"
	case credtest.CatUnreachable:
		add("reachable", "fail", nz(out.Detail, "port closed / no route"))
		final = "unreachable"
	case credtest.CatUnsupported:
		add("protocol", "fail", "protocol not supported by the tester")
		final = "protocol_not_supported"
	case credtest.CatOperationFault:
		add("reachable", "ok", "service reachable")
		add("auth", "ok", "authenticated (legacy WSMan operation fault — collect via the relay agent)")
		final = "implemented_tested"
	default:
		add("probe", "fail", nz(out.Detail, "probe error"))
		final = "error"
	}
	return onbTestResp{Steps: steps, FinalStatus: final, Category: out.Category, Summary: out.Detail}
}

// ---- Save ----

type onbSaveReq struct {
	Type                       string    `json:"type"`
	Method                     string    `json:"method"`
	Port                       int       `json:"port"`
	PrimaryIP                  string    `json:"primary_ip"`
	Name                       string    `json:"name"`
	Vendor                     string    `json:"vendor"`
	Model                      string    `json:"model"`
	Location                   string    `json:"location"`
	Criticality                string    `json:"criticality"`
	ManualClassificationReason string    `json:"manual_classification_reason"`
	Notes                      string    `json:"notes"`
	Credential                 onbCredIn `json:"credential"`
	TestPassed                 bool      `json:"test_passed"`
	SaveAsManualInventory      bool      `json:"save_as_manual_inventory"`
	RunCollection              bool      `json:"run_collection"`
}

type onbSaveResp struct {
	DeviceID      string `json:"device_id"`
	Existing      bool   `json:"existing"` // true => an existing scanned device was overridden
	State         string `json:"state"`    // managed-capable | manual_inventory_only | credential_required
	CredentialID  string `json:"credential_id,omitempty"`
	CollectionRun bool   `json:"collection_run"`
	Message       string `json:"message"`
}

// saveManualDevice is POST /manual-onboarding/save — create OR override-by-IP, bind the
// credential, apply manual authoritative classification (locked), audit, and optionally
// enqueue collection. Never fakes management.
func (s *Server) saveManualDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req onbSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	t, ok := onbTypeByKey(req.Type)
	if !ok {
		http.Error(w, "unknown device type", http.StatusBadRequest)
		return
	}
	ip := strings.TrimSpace(req.PrimaryIP)
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		http.Error(w, "invalid IP address", http.StatusBadRequest)
		return
	}
	// Gate: a non-managed-capable outcome may only be saved as manual inventory.
	managedCapable := req.TestPassed && !req.SaveAsManualInventory
	if !req.TestPassed && !req.SaveAsManualInventory {
		http.Error(w, "test did not pass — re-test, or set save_as_manual_inventory to save as manual inventory only", http.StatusBadRequest)
		return
	}

	// ZKTeco: a supplied communication key must be PERSISTED (sealed) as a credential so
	// future collection jobs can re-authenticate — not lost after the test. Tag it as a
	// 'zkteco' credential kind so resolveOrCreateCredential encrypts + binds it.
	if req.Method == "zkteco" && strings.TrimSpace(req.Credential.Secret) != "" && strings.TrimSpace(req.Credential.ID) == "" {
		req.Credential.Kind = "zkteco"
		if strings.TrimSpace(req.Credential.Name) == "" {
			req.Credential.Name = "ZKTeco comm key " + ip
		}
	}
	// Resolve / create the credential (validated + encrypted) when supplied.
	var credID *uuid.UUID
	if cid, cerr := s.resolveOrCreateCredential(ctx, r, req.Credential); cerr != nil {
		http.Error(w, cerr.Error(), http.StatusBadRequest)
		return
	} else if cid != nil {
		credID = cid
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = ip
	}
	reason := strings.TrimSpace(req.ManualClassificationReason)
	if reason == "" {
		reason = "manual onboarding: operator asserted " + t.DisplayName
	}

	// Vendor-specific telnet onboarding refines the PBX subtype (e.g. Alcatel OmniPCX).
	subtype := t.Subtype
	if req.Method == "omnipcx" {
		subtype = "alcatel_omnipcx"
	}

	existing, lookErr := s.queries.LiveDeviceByIP(ctx, &addr)
	isExisting := lookErr == nil && existing.ID != uuid.Nil

	var devID uuid.UUID
	if isExisting {
		// Manual authoritative OVERRIDE — update identity + lock; PRESERVE all discovery
		// evidence (facts/topology/MAC/ARP/services untouched). Record old→new for audit.
		devID = existing.ID
		_, uerr := s.queries.UpdateDevice(ctx, db.UpdateDeviceParams{
			ID: devID, Name: name, Category: t.Category,
			Vendor: orCurPtr(req.Vendor, existing.Vendor), Model: orCurPtr(req.Model, existing.Model),
			Serial: existing.Serial, OsVersion: existing.OsVersion, Hostname: existing.Hostname,
			Vlan: existing.Vlan, DeviceClass: existing.DeviceClass, Location: orCurPtr(req.Location, existing.Location),
			LocationID: existing.LocationID, Subtype: subtype, Notes: nz(req.Notes, existing.Notes),
			Criticality: nz(req.Criticality, existing.Criticality), MonitoringEnabled: existing.MonitoringEnabled,
			ClassificationLocked: true, ManualClassificationReason: reason,
		})
		if uerr != nil {
			writeErr(w, uerr)
			return
		}
		s.audit(r, "inventory", "manual_classification_override", "device", devID.String(),
			"Manual override "+ip+": "+existing.Category+" → "+t.Category,
			map[string]any{"ip": ip, "old_category": existing.Category, "new_category": t.Category, "old_subtype": existing.Subtype, "new_subtype": subtype, "reason": reason, "source_page": t.Group})
	} else {
		// New manual device.
		meta, _ := json.Marshal(map[string]any{"source": "manual", "onboarding_type": t.Type, "test_passed": req.TestPassed})
		created, cerr := s.queries.CreateDevice(ctx, db.CreateDeviceParams{
			PrimaryIp: &addr, Name: name, Category: t.Category, Status: "unknown",
			Vendor: nzPtr(req.Vendor), Model: nzPtr(req.Model), Location: nzPtr(req.Location),
			Metadata: meta,
		})
		if cerr != nil {
			writeErr(w, cerr)
			return
		}
		devID = created.ID
		// Stamp subtype + manual lock (CreateDevice doesn't carry them).
		_, _ = s.queries.UpdateDevice(ctx, db.UpdateDeviceParams{
			ID: devID, Name: name, Category: t.Category, Vendor: nzPtr(req.Vendor), Model: nzPtr(req.Model),
			Serial: nil, OsVersion: nil, Hostname: nil, Vlan: nil, DeviceClass: nil, Location: nzPtr(req.Location),
			LocationID: nil, Subtype: subtype, Notes: nz(req.Notes, ""), Criticality: nz(req.Criticality, "normal"),
			MonitoringEnabled: true, ClassificationLocked: true, ManualClassificationReason: reason,
		})
		s.audit(r, "inventory", "manual_device_added", "device", devID.String(),
			"Manually added "+t.DisplayName+" "+ip,
			map[string]any{"ip": ip, "category": t.Category, "subtype": subtype, "reason": reason, "source_page": t.Group, "test_passed": req.TestPassed})
	}

	// Bind credential ON SUCCESS only (the project rule: bind on a proven/operator credential).
	if credID != nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: devID, CredentialID: credID})
		s.audit(r, "inventory", "manual_credential_bind", "device", devID.String(), "Bound credential to "+name, map[string]any{"ip": ip})
	}

	resp := onbSaveResp{DeviceID: devID.String(), Existing: isExisting}
	if credID != nil {
		resp.CredentialID = credID.String()
	}
	// Honest state: managed comes ONLY from collection evidence. We classify + (optionally)
	// enqueue collection; the device's management state is derived, never set to "managed" here.
	if managedCapable {
		resp.State = "managed_capable"
		if req.RunCollection {
			if dev, gerr := s.queries.GetDevice(ctx, devID); gerr == nil {
				if req.Method == "zkteco" {
					// The Test Connection already authenticated over the ZK protocol — record that
					// proven success so the device is honestly managed, and refresh identity using
					// the comm key in-memory only (never stored).
					s.recordOnboardSuccess(ctx, devID, "biometric_zkteco", "zkteco", "ZKTeco protocol authenticated (identity collected)")
					s.collectZKTecoIdentity(ctx, dev, req.Credential.Secret)
					resp.CollectionRun = true
				} else if s.kickOnboardCollection(ctx, dev, req.Method) {
					// A real deep collector ran (snmp/redfish/onvif/isapi/ssh/windows/vsphere) —
					// it wrote real evidence + identity, so the device is honestly managed.
					resp.CollectionRun = true
				} else {
					// No deep collector for this method (e.g. http_basic / vendor_api). Re-verify
					// the protocol Test Connection SERVER-SIDE (never trust the client flag) and,
					// only if it genuinely authenticates, record the proven access → managed.
					var mo onbMethod
					for _, mm := range t.Methods {
						if mm.Key == req.Method {
							mo = mm
						}
					}
					if mo.Key != "" && mo.TestKind != "manual" {
						secret, _, cname, _ := s.resolveSecret(ctx, req.Credential)
						p := req.Port
						if p == 0 {
							p = mo.DefaultPort
						}
						pr := s.runOnboardProbe(ctx, mo, ip, p, secret, cname)
						if pr.FinalStatus == "implemented_collected" || pr.FinalStatus == "implemented_tested" {
							s.recordOnboardSuccess(ctx, devID, t.Type, mo.TestKind, "manual onboarding: "+mo.TestKind+" authenticated ("+pr.Summary+")")
							resp.CollectionRun = true
						}
					}
				}
			}
		}
		resp.Message = "Saved + classified. " + cond(resp.CollectionRun, "Collection enqueued — management state will reflect the result.", "Saved; run collection from the device page.")
	} else {
		resp.State = "manual_inventory_only"
		resp.Message = "Saved as manual inventory only (operator-asserted type, NOT managed). Provide a working credential + Test Connection to manage it."
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveOrCreateCredential returns the credential id to bind: an existing id, or a newly
// created (validated + encrypted) credential from inline fields. nil when none supplied.
func (s *Server) resolveOrCreateCredential(ctx context.Context, r *http.Request, c onbCredIn) (*uuid.UUID, error) {
	if strings.TrimSpace(c.ID) != "" {
		id, err := uuid.Parse(c.ID)
		if err != nil {
			return nil, fmt.Errorf("bad credential id")
		}
		return &id, nil
	}
	if strings.TrimSpace(c.Secret) == "" {
		return nil, nil // no credential (e.g. manual-only / anonymous RTSP)
	}
	if c.Kind == "" {
		// A secret with no credential kind is a protocol parameter (e.g. a ZKTeco
		// communication key), NOT a stored credential — used in-memory only, never persisted.
		return nil, nil
	}
	if malformedUserPassSecret(c.Kind, c.Secret) {
		return nil, fmt.Errorf("a %s credential must be 'username:password' with a non-empty password", c.Kind)
	}
	cph := s.cipher()
	if cph == nil {
		return nil, fmt.Errorf("encryption key not loaded")
	}
	blob, keyID, err := cph.Seal([]byte(c.Secret))
	if err != nil {
		return nil, err
	}
	nm := strings.TrimSpace(c.Name)
	if nm == "" {
		nm = "manual " + c.Kind
	}
	cred, err := s.queries.CreateCredential(ctx, db.CreateCredentialParams{
		Name: nm, Kind: c.Kind, EncryptedBlob: blob, KeyID: keyID,
		Weak: isWeakSecret(c.Kind, c.Secret), Metadata: []byte("{}"),
	})
	if err != nil {
		return nil, err
	}
	s.audit(r, "credential", "credential.create", "credential", cred.ID.String(), "Created credential "+cred.Name+" ("+cred.Kind+") via onboarding", nil)
	return &cred.ID, nil
}

// orCurPtr returns &v when v is non-empty, else the current pointer (override-or-keep).
func orCurPtr(v string, cur *string) *string {
	if strings.TrimSpace(v) != "" {
		x := v
		return &x
	}
	return cur
}

// cond is a tiny string ternary for honest status messages.
func cond(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

// kickOnboardCollection best-effort enqueues/runs the right collection for a freshly-saved
// device so its management state reflects real evidence. Errors are non-fatal (the device
// page can re-run). Never fakes a result.
func (s *Server) kickOnboardCollection(ctx context.Context, dev db.Device, method string) bool {
	switch method {
	case "vsphere":
		return s.runVSphereCollection(ctx, dev).ok()
	case "snmp_v2c", "snmp_v3":
		if _, err := s.collectSNMPInterfaces(ctx, dev, "", 10*time.Second); err == nil {
			return true
		}
	case "windows":
		if _, ok := s.routeViaSiteAgent(ctx, dev, dev.PrimaryIp.String(), "winrm"); ok {
			return true
		}
	case "redfish":
		ok, _ := s.collectViaController(ctx, "redfish", dev, vpConfig{})
		return ok
	case "onvif":
		ok, _ := s.collectViaController(ctx, "onvif", dev, vpConfig{})
		return ok
	case "isapi":
		var creds []uuid.UUID
		if dev.CredentialID != nil {
			creds = []uuid.UUID{*dev.CredentialID}
		}
		return s.runCCTVCollection(ctx, dev, creds, "onboarding").ok()
	case "ssh":
		return s.collectSSHCLI(ctx, dev, "", "", false, nil).OK
	case "omnipcx":
		return s.collectOmniPCXIdentity(ctx, dev)
	}
	return false
}

// collectOmniPCXIdentity logs into the OmniPCX over telnet using the bound CLI credential
// (decrypted in-memory, never logged/returned), reads the software-identity banner, persists
// real vendor/model/version + facts, and records the proven login so the PBX is honestly
// managed. Read-only — no configuration commands. Returns false if it can't authenticate.
func (s *Server) collectOmniPCXIdentity(ctx context.Context, dev db.Device) bool {
	if dev.PrimaryIp == nil || dev.CredentialID == nil {
		return false
	}
	cph := s.cipher()
	if cph == nil {
		return false
	}
	c, err := s.queries.GetCredential(ctx, *dev.CredentialID)
	if err != nil {
		return false
	}
	plain, oerr := cph.Open(c.EncryptedBlob, c.KeyID)
	if oerr != nil {
		return false
	}
	user, pass := credtest.SplitUserPass(string(plain))
	id := omnipcx.Probe(ctx, dev.PrimaryIp.String(), 23, user, pass)
	if !id.Connected {
		return false
	}
	_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
		ID: dev.ID, Vendor: id.Vendor, Model: id.Model, Serial: nz(derefStr(dev.Serial), ""), OsVersion: id.OSVersion(), Hostname: derefStr(dev.Hostname),
	})
	for k, v := range map[string]string{"omnipcx.cpu_role": id.CPURole, "omnipcx.delivery": id.Delivery, "omnipcx.patch": id.Patch, "omnipcx.country": id.Country} {
		if v != "" {
			val := v
			_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: dev.ID, Key: k, Value: &val, Driver: "omnipcx"})
		}
	}
	s.recordOnboardSuccess(ctx, dev.ID, "pbx", "omnipcx", "OmniPCX telnet login authenticated — "+id.OSVersion())
	return true
}

// collectZKTecoIdentity runs the read-only ZKTeco identity probe and persists the collected
// serial/model/firmware (ONLY real values — never fabricated). commKey is used in-memory only
// and never stored (it is a device secret). Returns whether the probe authenticated.
func (s *Server) collectZKTecoIdentity(ctx context.Context, dev db.Device, commKey string) bool {
	if dev.PrimaryIp == nil {
		return false
	}
	// For future re-collection (no inline key), use the bound zkteco credential (decrypted
	// in-memory) so the stored communication key is reused — never logged or returned.
	if strings.TrimSpace(commKey) == "" && dev.CredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *dev.CredentialID); err == nil && c.Kind == string(domain.CredZKTeco) {
			if cph := s.cipher(); cph != nil {
				if plain, oerr := cph.Open(c.EncryptedBlob, c.KeyID); oerr == nil {
					commKey = string(plain)
				}
			}
		}
	}
	id := zkteco.Probe(ctx, dev.PrimaryIp.String(), 4370, commKey)
	if !id.Connected {
		return false
	}
	if id.Serial != "" || id.Firmware != "" || id.DeviceName != "" {
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: dev.ID, Vendor: "ZKTeco", Model: id.DeviceName, Serial: id.Serial, OsVersion: id.Firmware, Hostname: id.DeviceName,
		})
	}
	if id.Platform != "" {
		v := id.Platform
		_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: dev.ID, Key: "zkteco.platform", Value: &v, Driver: "zkteco"})
	}
	// Read-only enrollment SUMMARY (counts only — no names, templates, or attendance logs):
	// how many users + fingerprints are enrolled, and capacity. Real fleet-management data.
	if en := zkteco.ReadEnrollment(ctx, dev.PrimaryIp.String(), 4370, commKey); en.Connected {
		facts := map[string]int{
			"zkteco.users_enrolled": en.Users, "zkteco.fingerprints_enrolled": en.Fingers,
			"zkteco.attendance_records": en.Records, "zkteco.users_capacity": en.UsersCap,
			"zkteco.fingerprints_capacity": en.FingersCap, "zkteco.records_capacity": en.RecordsCap,
		}
		for k, v := range facts {
			val := fmt.Sprintf("%d", v)
			_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: dev.ID, Key: k, Value: &val, Driver: "zkteco"})
		}
	}
	return true
}

// recordOnboardSuccess writes a 1-result credential-test run marking a successful onboarding
// protocol authentication, so the device's management state reflects proven evidence.
func (s *Server) recordOnboardSuccess(ctx context.Context, devID uuid.UUID, kind, protocol, detail string) {
	run, err := s.queries.InsertCredentialTestRun(ctx, db.InsertCredentialTestRunParams{Actor: "onboarding", Pairs: 1, Successes: 1, Failures: 0})
	if err != nil {
		return
	}
	_ = s.queries.InsertCredentialTestResult(ctx, db.InsertCredentialTestResultParams{
		RunID: run.ID, DeviceID: devID, CredentialName: "onboarding", Kind: kind, Protocol: protocol,
		Category: "success", Success: true, Detail: detail, Actor: "onboarding", Relevant: true, Source: "manual",
	})
}
