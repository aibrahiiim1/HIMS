package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/isapi"
	rf "github.com/coralsearesorts/hims/internal/redfish"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Device-level AUTHENTICATED Redfish path for an out-of-band controller (iLO/iDRAC).
// This is the operator-driven counterpart to the unauthenticated ServiceRoot probe that
// discovery runs: it uses ONE explicitly-chosen (or bound) http_basic/vendor_api
// credential — never a spray of every stored login, because iLO/iDRAC lock accounts on
// repeated failed logins (the BMC-lockout lesson). It writes bmc_info ONLY after a real
// authenticated collection succeeds, and records a proven auth rejection as
// credential_failed — the two states the BMC page needs to be honest.

type bmcRedfishReq struct {
	CredentialID string `json:"credential_id"`
}

// redfishClientForCred builds a Redfish client for ip using ONE decrypted http_basic/
// vendor_api credential. The secret is decrypted in-memory only and never logged or
// returned. The permissive TLS client handles the self-signed / legacy-TLS certs BMCs
// ship on the management LAN.
func (s *Server) redfishClientForCred(ctx context.Context, ip string, credID uuid.UUID) (*rf.Client, string, error) {
	dec, err := s.scanDecrypt(ctx, credID)
	if err != nil {
		return nil, "", fmt.Errorf("could not decrypt the selected credential")
	}
	if dec.Kind != domain.CredHTTPBasic && dec.Kind != domain.CredVendorAPI {
		return nil, "", fmt.Errorf("selected credential is %s — Redfish needs an http_basic (or vendor_api) login", dec.Kind)
	}
	u, p := credtest.SplitUserPass(dec.Community)
	if p == "" {
		return nil, "", fmt.Errorf("selected credential has no password")
	}
	name := ""
	if c, e := s.queries.GetCredential(ctx, credID); e == nil {
		name = c.Name
	}
	return rf.NewClient("https://"+ip, u, p, isapi.PermissiveClient(20*time.Second)), name, nil
}

// resolveRedfishCred picks the credential to use: the request's explicit credential_id,
// else the device's BOUND credential IF it is an http_basic/vendor_api login. Returns
// uuid.Nil when nothing usable is bound — the honest credential_required case (a bound
// SNMP community can NOT collect Redfish).
func (s *Server) resolveRedfishCred(ctx context.Context, req bmcRedfishReq, dev db.Device) uuid.UUID {
	if id, err := uuid.Parse(strings.TrimSpace(req.CredentialID)); err == nil && id != uuid.Nil {
		return id
	}
	// The Redfish cred remembered from a prior successful collect (stored as a fact so it
	// survives even when the PRIMARY binding stays SNMP for an HPE iLO's health refresh).
	if facts, err := s.queries.ListDeviceFacts(ctx, dev.ID); err == nil {
		for _, f := range facts {
			if f.Key == "redfish.credential_id" {
				if id, e := uuid.Parse(derefStr(f.Value)); e == nil && id != uuid.Nil {
					return id
				}
			}
		}
	}
	if dev.CredentialID != nil {
		if c, e := s.queries.GetCredential(ctx, *dev.CredentialID); e == nil &&
			(c.Kind == string(domain.CredHTTPBasic) || c.Kind == string(domain.CredVendorAPI)) {
			return *dev.CredentialID
		}
	}
	return uuid.Nil
}

// bindRedfishCred records the working Redfish credential. It PRESERVES an HPE iLO's bound
// SNMP v2c credential (the accepted SNMP health-refresh sweep needs it) by storing the
// Redfish cred as a fact instead of overwriting the primary binding. Any other BMC (a Dell
// iDRAC, whose management channel IS Redfish) binds it as the primary credential.
func (s *Server) bindRedfishCred(ctx context.Context, dev db.Device, credID uuid.UUID) {
	s.setDeviceFact(ctx, dev.ID, "redfish.credential_id", credID.String(), "redfish")
	preserveSNMP := false
	if dev.CredentialID != nil {
		if c, e := s.queries.GetCredential(ctx, *dev.CredentialID); e == nil && c.Kind == string(domain.CredSNMPv2c) {
			v := strings.ToLower(derefStr(dev.Vendor))
			preserveSNMP = strings.Contains(v, "hp") || strings.Contains(v, "hewlett") || strings.Contains(v, "compaq")
		}
	}
	if !preserveSNMP {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: dev.ID, CredentialID: &credID})
	}
}

// testBMCRedfish is POST /devices/{id}/test-redfish — a read-only Test Connection for a
// selected credential. It gates on /redfish/v1/Systems (which REQUIRES auth; the root is
// anonymous) so an anonymous root read can never be mistaken for a successful login. It
// persists NOTHING and binds NOTHING.
func (s *Server) testBMCRedfish(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if dev.PrimaryIp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "no_ip", "detail": "device has no IP"})
		return
	}
	var req bmcRedfishReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	credID := s.resolveRedfishCred(ctx, req, dev)
	if credID == uuid.Nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "credential_required",
			"detail": "Select an http_basic/Redfish credential to test — the bound credential (if any) is not a Redfish login."})
		return
	}
	client, credName, cerr := s.redfishClientForCred(ctx, dev.PrimaryIp.String(), credID)
	if cerr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "credential_required", "detail": cerr.Error()})
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var sys map[string]any
	if aerr := client.GetJSON(cctx, "/redfish/v1/Systems", &sys); aerr != nil {
		reason, detail := categorizeCollectErr("redfish", aerr.Error())
		state := reason
		if reason == "auth_failed" {
			state = "credential_failed"
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": state, "detail": detail, "credential": credName})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "auth_ok",
		"detail": "Redfish login succeeded (/Systems authorized). Use Collect Now to gather full inventory.", "credential": credName})
}

// collectBMCRedfish is POST /devices/{id}/collect-bmc-redfish — AUTHENTICATED Redfish
// collection with ONE credential. On success it writes bmc_info (identity + health +
// sensors) on THIS device (category stays bmc), enriches identity, binds the credential,
// and the BMC page reads redfish_status=collected. A proven auth rejection is recorded as
// redfish.collect=auth_failed (→ credential_failed) and writes NO bmc_info. A transport
// failure is neither — it stays reachable/credential_required. NEVER a spray.
func (s *Server) collectBMCRedfish(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if dev.PrimaryIp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "no_ip", "detail": "device has no IP"})
		return
	}
	ip := dev.PrimaryIp.String()
	var req bmcRedfishReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	credID := s.resolveRedfishCred(ctx, req, dev)
	if credID == uuid.Nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "credential_required",
			"detail": "No Redfish-capable credential. Bind or select an http_basic/Redfish credential, then Collect Now."})
		return
	}
	client, credName, cerr := s.redfishClientForCred(ctx, ip, credID)
	if cerr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "credential_required", "detail": cerr.Error()})
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	// Auth gate on /Systems (requires auth). A real rejection here is credential_failed;
	// a transport error is NOT (the controller is reachable, just not collected).
	var sys map[string]any
	if aerr := client.GetJSON(cctx, "/redfish/v1/Systems", &sys); aerr != nil {
		reason, detail := categorizeCollectErr("redfish", aerr.Error())
		if reason == "auth_failed" {
			s.setDeviceFact(cctx, dev.ID, "redfish.collect", "auth_failed", "redfish")
			s.audit(r, "inventory", "bmc_redfish_auth_failed", "device", id.String(), "Redfish credential rejected by "+ip, map[string]any{"ip": ip, "credential": credName})
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "credential_failed", "detail": detail, "credential": credName})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": reason, "detail": detail})
		return
	}

	// Authenticated — walk the Redfish tree and persist REAL inventory.
	facts, ferr := rf.Collect(cctx, client)
	if ferr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "collection_error", "detail": "Redfish authenticated but inventory read failed: " + shortErr(ferr)})
		return
	}
	poll := time.Now()
	_ = s.queries.UpsertBMCInfo(ctx, db.UpsertBMCInfoParams{
		DeviceID: dev.ID, Vendor: nzp(facts.Vendor), ControllerKind: nzp(facts.ControllerKind),
		Model: nzp(facts.Model), Serial: nzp(facts.Serial), FirmwareVersion: nzp(facts.FirmwareVersion),
		PowerState: nzp(facts.PowerState), Health: nzp(facts.Health), LastSeenAt: poll,
	})
	for _, sn := range facts.Sensors {
		_ = s.queries.UpsertBMCSensor(ctx, db.UpsertBMCSensorParams{
			DeviceID: dev.ID, Kind: sn.Kind, Name: sn.Name, Status: nzp(sn.Status),
			Reading: f64p(sn.Reading), Unit: nzp(sn.Unit), HasReading: sn.HasReading,
			CollectionSource: "redfish", LastSeenAt: poll,
		})
	}
	if len(facts.Sensors) > 0 {
		_ = s.queries.DeleteStaleBMCSensors(ctx, db.DeleteStaleBMCSensorsParams{DeviceID: dev.ID, LastSeenAt: poll, CollectionSource: "redfish"})
	}
	// Enrich the device row identity with the AUTHENTICATED Redfish values (COALESCE in the
	// query never wipes a proven field). Never changes category — this is a bmc. The device
	// serial is PRESERVED when already set (e.g. a Dell ServiceTag from the ServiceRoot probe
	// is the canonical Dell serial); the Redfish ComputerSystem SerialNumber is kept in
	// bmc_info. Only adopt it onto the row when the device had no serial at all.
	rowSerial := ""
	if strings.TrimSpace(derefStr(dev.Serial)) == "" {
		rowSerial = facts.Serial
	}
	_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
		ID: dev.ID, Vendor: facts.Vendor, Model: facts.Model, Serial: rowSerial,
		OsVersion: facts.FirmwareVersion, Hostname: derefStr(dev.Hostname),
	})
	// Record the working Redfish credential (preserving an HPE iLO's SNMP binding for the
	// health-refresh sweep) — bind-on-success, never before.
	s.bindRedfishCred(ctx, dev, credID)
	s.setDeviceFact(ctx, dev.ID, "redfish.collect", "collected", "redfish")
	s.audit(r, "inventory", "bmc_redfish_collected", "device", id.String(),
		"Collected authenticated Redfish inventory for "+ip, map[string]any{"ip": ip, "credential": credName, "sensors": len(facts.Sensors)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "collected",
		"detail":     fmt.Sprintf("Redfish inventory collected: %s %s, health=%s, %d sensors.", facts.Vendor, nz(facts.Model, facts.ControllerKind), nz(facts.Health, "?"), len(facts.Sensors)),
		"credential": credName})
}

// setDeviceFact upserts one string device fact (best-effort).
func (s *Server) setDeviceFact(ctx context.Context, devID uuid.UUID, key, value, driver string) {
	v := value
	_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: devID, Key: key, Value: &v, Driver: driver})
}

func nzp(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func f64p(f float64) *float64 { return &f }
