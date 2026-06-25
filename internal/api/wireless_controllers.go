package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// enrichWirelessControllerDevice writes a collected controller's identity back
// onto the device row so the Wireless Controllers list shows real
// Vendor/Model/OS/Driver/Status values instead of blanks/"unknown". All identity
// writes are COALESCE-safe (a blank field never wipes an existing value), the
// Driver column is stamped with the active collector's registry key, and Status
// flips to "up" ONLY on a successful poll — never fabricated.
func (s *Server) enrichWirelessControllerDevice(ctx context.Context, deviceID uuid.UUID, vendorType, vendorLabel, model, serial, osVersion, hostname string, reachable bool) {
	_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
		ID: deviceID, Vendor: vendorLabel, Model: model, Serial: serial, OsVersion: osVersion, Hostname: hostname,
	})
	_ = s.queries.SetDeviceDriver(ctx, db.SetDeviceDriverParams{ID: deviceID, Driver: wlVendorKeyForProfileType(vendorType)})
	if reachable {
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: deviceID, Status: "up"})
	}
}

// recordWirelessHealth writes one honest per-capability verdict for the device's
// last collection, driven by the driver catalog: a capability the driver does
// not implement is "not_implemented"; one it implements is "collected" when rows
// came back, "endpoint_not_exposed" when the API/firmware returned none, and
// "auth_failed" when login never succeeded. counts is keyed by capability key
// (aps/ssids/clients/radios/health/firmware). This is the source of truth behind
// the per-feature badges — it never claims a capability that did not run.
func (s *Server) recordWirelessHealth(ctx context.Context, deviceID uuid.UUID, vendorKey, source string, authed bool, counts map[string]int) {
	ven, ok := wlVendorByKey(vendorKey)
	if !ok {
		return
	}
	for _, c := range ven.Capabilities {
		status, detail := capCollected, ""
		switch {
		case counts[c.Key] > 0:
			// Real rows win over any declared status — proof of collection.
			status, detail = capCollected, itoaN(counts[c.Key])+" "+c.Key+" row(s) via "+source
		case c.Status == capUnsupportedByDevice || c.Status == capNotImplemented || c.Status == capCollectorPending ||
			c.Status == capLiveValidationPending || c.Status == capExternalDependency:
			// Declared non-collected-yet: record the status as-is with its reason.
			// For implemented_live_validation_pending this means "the collector ran
			// but returned no rows (no live data to confirm the shape)" — NOT
			// "developer work remaining". Real rows (the case above) flip it to
			// collected automatically.
			status, detail = c.Status, nz(c.Reason, "no "+c.Key+" path on "+ven.DisplayName)
		case !authed:
			status, detail = capAuthFailed, "controller login did not succeed — "+c.Key+" not collected"
		default:
			// Endpoint-level honesty: the driver implements this capability and
			// authenticated, but THIS endpoint returned nothing on this firmware/API
			// version (names the exact capability so the operator can act).
			status, detail = capEndpointNotExposed, ven.DisplayName+" "+c.Key+" endpoint returned no rows on this firmware/API version"
		}
		_ = s.queries.UpsertWirelessCapabilityHealth(ctx, db.UpsertWirelessCapabilityHealthParams{
			ControllerDeviceID: deviceID, Capability: c.Key, Status: string(status),
			Detail: detail, RowCount: int32(counts[c.Key]), Source: source,
		})
	}
}

// addWirelessControllerReq is the one-step "Inventory → Wireless → Add controller"
// payload: IP + vendor + admin credential. The password is sealed into a
// credentials row (never stored plaintext); a device + enabled vendor profile are
// created and an immediate REST/XML collection is kicked.
type addWirelessControllerReq struct {
	Vendor       string  `json:"vendor"` // a wireless-registry vendor key (see wireless_registry.go)
	IP           string  `json:"ip"`
	Name         string  `json:"name"`
	LocationID   *string `json:"location_id"`
	Username     string  `json:"username"`
	Password     string  `json:"password"`
	Port         int     `json:"port"`          // optional (defaults to the vendor's registry DefaultPort)
	APIBase      string  `json:"api_base"`      // optional (extreme_xcc / ruckus_sz)
	Site         string  `json:"site"`          // optional (unifi / omada / aruba controller site name)
	ControllerID string  `json:"controller_id"` // omada controllerId
	SSLVerify    bool    `json:"ssl_verify"`
}

// addWirelessController handles POST /wireless/controllers.
func (s *Server) addWirelessController(w http.ResponseWriter, r *http.Request) {
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not configured (set HIMS_ENCRYPTION_KEY)", http.StatusServiceUnavailable)
		return
	}
	var req addWirelessControllerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Vendor = strings.TrimSpace(req.Vendor)
	// Validate against the single registry — the form dropdown and this allow-list
	// are the same table, so they can never drift. unsupported vendors are rejected.
	ven, known := wlVendorByKey(req.Vendor)
	if !known || ven.Status == wlStatusUnsupported {
		http.Error(w, "unknown or unsupported wireless vendor: "+req.Vendor, http.StatusBadRequest)
		return
	}
	// Required vendor-specific fields (e.g. Omada controllerId) are enforced here so
	// a working vendor is never persisted with a profile that cannot collect.
	for _, fld := range ven.Fields {
		if fld.Required && strings.TrimSpace(reqFieldValue(req, fld.Key)) == "" {
			http.Error(w, ven.DisplayName+" requires "+fld.Label, http.StatusBadRequest)
			return
		}
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(req.IP))
	if err != nil {
		http.Error(w, "invalid ip", http.StatusBadRequest)
		return
	}
	if msg := missingCredential(ven, req); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	port := req.Port
	if port <= 0 {
		port = ven.DefaultPort
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Wireless Controller " + ip.String()
	}
	locID := parseUUIDPtr(req.LocationID)
	ctx := r.Context()

	// 1. Seal the admin credential as "user:pass" (the SplitUserPass format).
	blob, keyID, err := cph.Seal([]byte(strings.TrimSpace(req.Username) + ":" + req.Password))
	if err != nil {
		writeErr(w, err)
		return
	}

	// Vendor label hint — recorded on the device so it carries a vendor even before
	// any SNMP fingerprint, and so the SNMP fallback has a vendor signal.
	vendorLabel := ven.deviceVendor

	// 2. Find-or-create the controller device. idx_devices_unique_primary_ip is a
	// GLOBAL unique index on primary_ip, so we MUST reconcile by IP — not just by
	// (ip, location) — otherwise a controller already discovered under a different
	// (or NULL) location collides with a 23505 instead of being reused. Re-adding
	// an existing controller reuses its row and re-points it at REST/XML; it never
	// duplicates and never errors.
	dev, derr := s.queries.LiveDeviceByIPAndLocation(ctx, db.LiveDeviceByIPAndLocationParams{PrimaryIp: &ip, LocationID: locID})
	if derr != nil {
		if byIP, e2 := s.queries.LiveDeviceByIP(ctx, &ip); e2 == nil {
			dev = byIP
		} else {
			dev, derr = s.queries.CreateDevice(ctx, db.CreateDeviceParams{
				LocationID: locID, PrimaryIp: &ip, Name: name, Category: string(domain.CatWirelessController),
				Vendor: &vendorLabel, Status: "unknown", Metadata: []byte(`{"source":"wireless_controller_add"}`),
			})
			if derr != nil {
				writeErr(w, derr)
				return
			}
		}
	}
	// A reused device must be treated as a wireless controller so it routes to
	// REST/XML and surfaces under Inventory → Wireless. The query is a guarded
	// no-op when the operator locked classification, and preserves os/class fields.
	if dev.Category != string(domain.CatWirelessController) {
		if updated, cerr := s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
			ID: dev.ID, Category: string(domain.CatWirelessController),
			OsFamily: dev.OsFamily, DeviceClass: dev.DeviceClass,
			ConfidenceScore: dev.ConfidenceScore, ClassificationEvidence: dev.ClassificationEvidence,
		}); cerr == nil {
			dev = updated
		}
	}
	devID := dev.ID

	// Build the profile config from the vendor's registry-declared fields, applying
	// each field's default when the operator left it blank. This is the only place
	// vendor parameters are mapped, so a new vendor needs no code here — just a row.
	cfg := map[string]any{"ssl_verify": req.SSLVerify}
	for _, fld := range ven.Fields {
		val := strings.TrimSpace(reqFieldValue(req, fld.Key))
		if val == "" {
			val = fld.Default
		}
		if val != "" {
			cfg[fld.Key] = val
		}
	}
	cfgJSON, _ := json.Marshal(cfg)
	vendorType := ven.profileVendorType
	targetURL := "https://" + ip.String() + ":" + strconv.Itoa(port)
	credName := name + " wireless admin @" + ip.String()

	// 3. Idempotent: re-adding the same controller (same device + vendor) updates the
	// existing profile + re-seals its credential rather than creating duplicates.
	var prof db.VendorConnectionProfile
	if existing, gerr := s.queries.GetVendorProfileForDeviceVendor(ctx, db.GetVendorProfileForDeviceVendorParams{DeviceID: &devID, VendorType: vendorType}); gerr == nil {
		credID := existing.CredentialID
		if credID != nil {
			if err := s.queries.UpdateCredentialSecret(ctx, db.UpdateCredentialSecretParams{ID: *credID, EncryptedBlob: blob, KeyID: keyID}); err != nil {
				writeErr(w, err)
				return
			}
		} else {
			c, cerr := s.queries.CreateCredential(ctx, db.CreateCredentialParams{Name: credName, Kind: string(domain.CredHTTPBasic), EncryptedBlob: blob, KeyID: keyID, Weak: isWeakSecret(string(domain.CredHTTPBasic), req.Password), Metadata: []byte("{}")})
			if cerr != nil {
				writeErr(w, cerr)
				return
			}
			credID = &c.ID
		}
		prof, err = s.queries.UpdateVendorProfile(ctx, db.UpdateVendorProfileParams{
			ID: existing.ID, Name: name, VendorType: vendorType, TargetUrl: targetURL,
			CredentialID: credID, LocationID: locID, DeviceID: &devID, Config: cfgJSON, Enabled: true,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
	} else {
		cred, cerr := s.queries.CreateCredential(ctx, db.CreateCredentialParams{
			Name: credName, Kind: string(domain.CredHTTPBasic), EncryptedBlob: blob, KeyID: keyID,
			Weak: isWeakSecret(string(domain.CredHTTPBasic), req.Password), Metadata: []byte("{}"),
		})
		if cerr != nil {
			if isUniqueViolation(cerr) {
				http.Error(w, "a wireless-admin credential with this name already exists — pick a different controller name", http.StatusConflict)
				return
			}
			writeErr(w, cerr)
			return
		}
		credID := cred.ID
		prof, err = s.queries.CreateVendorProfile(ctx, db.CreateVendorProfileParams{
			Name: name, VendorType: vendorType, TargetUrl: targetURL,
			CredentialID: &credID, LocationID: locID, DeviceID: &devID, Config: cfgJSON, Enabled: true,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
	}
	// A controller has exactly ONE wireless vendor. If a DIFFERENT-vendor profile
	// was previously bound to this device (e.g. added as Extreme by mistake, now
	// re-added as Ruckus), retire it so the routing authority can never pick the
	// wrong collector. This realises the operator's "delete the current one and
	// add it" — the next collection rewrites the controller source to this vendor.
	// Registry-driven so any vendor combination is reconciled, not just the original
	// extreme/ruckus pair.
	for _, otherVendor := range wirelessProfileVendorTypes() {
		if otherVendor == vendorType {
			continue
		}
		if old, oerr := s.queries.GetVendorProfileForDeviceVendor(ctx, db.GetVendorProfileForDeviceVendorParams{DeviceID: &devID, VendorType: otherVendor}); oerr == nil && old.Enabled {
			_, _ = s.queries.UpdateVendorProfile(ctx, db.UpdateVendorProfileParams{
				ID: old.ID, Name: old.Name, VendorType: old.VendorType, TargetUrl: old.TargetUrl,
				CredentialID: old.CredentialID, LocationID: old.LocationID, DeviceID: old.DeviceID,
				Config: old.Config, Enabled: false,
			})
		}
	}

	s.audit(r, "inventory", "wireless.add_controller", "device", dev.ID.String(),
		"Added/updated "+req.Vendor+" wireless controller "+name, map[string]any{"vendor": req.Vendor, "ip": ip.String()})

	// 4. Kick the REST/XML collection asynchronously — the primary collection path.
	go func(p db.VendorConnectionProfile, d db.Device) {
		bg, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_, _, _ = s.collectWirelessForDevice(bg, d, &p)
	}(prof, dev)

	// Honest response: a working vendor kicked a real collection; a collector_pending
	// vendor (e.g. Aruba) is tracked + classified but never claims AP/client fetch.
	detail := "Controller added; collection started (primary). It will appear under Inventory → Wireless shortly."
	if ven.Status != wlStatusWorking {
		detail = "Controller added + classified as " + ven.DisplayName + ". " + nz(ven.NextAction, "Deep collection is not available for this vendor yet.")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"device_id":  dev.ID.String(),
		"profile_id": prof.ID.String(),
		"source":     vendorType,
		"status":     string(ven.Status),
		"detail":     detail,
	})
}

// reqFieldValue maps a registry field key to its value on the request. New
// vendor fields must be added here AND as a struct field on
// addWirelessControllerReq (kept tiny on purpose — only three parameters exist).
func reqFieldValue(req addWirelessControllerReq, key string) string {
	switch key {
	case "api_base":
		return req.APIBase
	case "site":
		return req.Site
	case "controller_id":
		return req.ControllerID
	}
	return ""
}

// wirelessCollectorVendorTypes is the ordered set of profile vendor_types this
// authority can run, derived from the registry so it can never fall out of step
// with what the form/handler persist. ZD/XCC have bespoke Web-XML/REST
// collectors; the rest route through the generic vendor-profile collector;
// collector_pending vendors (Aruba) are handled honestly without faking data.
func wirelessCollectorVendorTypes() []string {
	out := make([]string, 0, len(wirelessVendors))
	for _, v := range wirelessVendors {
		out = append(out, v.profileVendorType)
	}
	return out
}

// collectWirelessForDevice is the single authority for wireless collection
// precedence (runbook §4.5): if an enabled wireless vendor profile is bound to
// the device, it runs the matching collector (PRIMARY) and returns handled=true;
// otherwise it returns handled=false so the caller falls back to the existing
// SNMP/SSH MIB collection (unchanged). Call it from the manual "collect now"
// action and the add-controller flow so precedence can never drift. Pass a known
// profile to skip the lookup (the add-controller flow already has it).
func (s *Server) collectWirelessForDevice(ctx context.Context, dev db.Device, known *db.VendorConnectionProfile) (handled, ok bool, detail string) {
	run := func(p db.VendorConnectionProfile) (bool, string) {
		var rok bool
		var rdetail string
		switch p.VendorType {
		case "extreme_xcc":
			rok, rdetail = s.collectXCCProfile(ctx, p, dev)
		case "ruckus_zd":
			rok, rdetail = s.collectRuckusZDProfile(ctx, p, dev)
		case "wireless_unifi", "wireless_omada", "wireless_ruckus", "wireless_extreme",
			"wireless_aruba", "wireless_aruba_os10", "wireless_aruba_central":
			rok, rdetail = s.collectWirelessProfile(ctx, p, dev)
		default:
			return false, ""
		}
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: p.ID, LastTestOk: &rok, LastTestDetail: rdetail})
		_ = s.queries.SetVendorProfileCollection(ctx, db.SetVendorProfileCollectionParams{ID: p.ID, LastCollectionDetail: rdetail})
		return rok, rdetail
	}

	isWireless := func(vt string) bool {
		for _, t := range wirelessCollectorVendorTypes() {
			if t == vt {
				return true
			}
		}
		return false
	}

	if known != nil && known.Enabled && isWireless(known.VendorType) {
		ok, detail = run(*known)
		return true, ok, detail
	}
	for _, vt := range wirelessCollectorVendorTypes() {
		profs, err := s.queries.ResolveVendorProfiles(ctx, db.ResolveVendorProfilesParams{
			VendorType: vt, DeviceID: &dev.ID, LocationID: dev.LocationID,
		})
		if err != nil || len(profs) == 0 || !profs[0].Enabled {
			continue
		}
		ok, detail = run(profs[0])
		return true, ok, detail
	}
	return false, false, ""
}
