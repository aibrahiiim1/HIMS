package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/vim25/soap"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/cucm"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/extreme"
	"github.com/coralsearesorts/hims/internal/omada"
	"github.com/coralsearesorts/hims/internal/onvif"
	"github.com/coralsearesorts/hims/internal/ruckus"
	"github.com/coralsearesorts/hims/internal/ruckuszd"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/coralsearesorts/hims/internal/unifi"
	"github.com/coralsearesorts/hims/internal/vsphere"
)

// Vendor Connection Profiles — the operator-facing way to close a config gate.
// A profile pairs a stored credential with a target URL + vendor connection
// params (vCenter URL/insecure; UniFi site / Omada controller-id / Ruckus
// apiBase; CUCM AXL version) and an optional site/device binding, so the scan
// (and a manual Test/Run) can actually authenticate + collect these integrations.
// Secrets never leave the credentials table; this file decrypts in-memory only.

type vpConfig struct {
	Site         string `json:"site,omitempty"`
	ControllerID string `json:"controller_id,omitempty"`
	APIBase      string `json:"api_base,omitempty"`
	Version      string `json:"version,omitempty"`
	Insecure     bool   `json:"insecure,omitempty"`
	// SSLVerify (extreme_xcc): when true, validate the controller's TLS cert.
	// Default false (mgmt-LAN appliances ship self-signed certs).
	SSLVerify bool `json:"ssl_verify,omitempty"`
}

func parseVPConfig(b []byte) vpConfig {
	var c vpConfig
	if len(b) > 0 {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// insecureDoer is an HTTP client that tolerates self-signed vendor certs.
func insecureDoer(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10}},
	}
}

// --- DTO (no secrets) --------------------------------------------------------

type vendorProfileDTO struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	VendorType           string         `json:"vendor_type"`
	TargetURL            string         `json:"target_url"`
	CredentialID         string         `json:"credential_id,omitempty"`
	CredentialName       string         `json:"credential_name,omitempty"`
	LocationID           string         `json:"location_id,omitempty"`
	DeviceID             string         `json:"device_id,omitempty"`
	Config               map[string]any `json:"config"`
	Enabled              bool           `json:"enabled"`
	LastTestAt           string         `json:"last_test_at,omitempty"`
	LastTestOK           *bool          `json:"last_test_ok,omitempty"`
	LastTestDetail       string         `json:"last_test_detail,omitempty"`
	LastCollectionAt     string         `json:"last_collection_at,omitempty"`
	LastCollectionDetail string         `json:"last_collection_detail,omitempty"`
	Status               string         `json:"status"`
}

func (s *Server) toVendorProfileDTO(ctx context.Context, p db.VendorConnectionProfile) vendorProfileDTO {
	d := vendorProfileDTO{
		ID: p.ID.String(), Name: p.Name, VendorType: p.VendorType, TargetURL: p.TargetUrl,
		CredentialID: uuidPtrStr(p.CredentialID), LocationID: uuidPtrStr(p.LocationID),
		DeviceID: uuidPtrStr(p.DeviceID), Enabled: p.Enabled, LastTestOK: p.LastTestOk,
		LastTestDetail: p.LastTestDetail, LastCollectionDetail: p.LastCollectionDetail, Status: p.Status,
	}
	d.Config = map[string]any{}
	if len(p.Config) > 0 {
		_ = json.Unmarshal(p.Config, &d.Config)
	}
	if p.LastTestAt != nil {
		d.LastTestAt = p.LastTestAt.Format(time.RFC3339)
	}
	if p.LastCollectionAt != nil {
		d.LastCollectionAt = p.LastCollectionAt.Format(time.RFC3339)
	}
	if p.CredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *p.CredentialID); err == nil {
			d.CredentialName = c.Name
		}
	}
	return d
}

// --- CRUD --------------------------------------------------------------------

type vendorProfileReq struct {
	Name         string         `json:"name"`
	VendorType   string         `json:"vendor_type"`
	TargetURL    string         `json:"target_url"`
	CredentialID string         `json:"credential_id"`
	LocationID   string         `json:"location_id"`
	DeviceID     string         `json:"device_id"`
	Config       map[string]any `json:"config"`
	Enabled      *bool          `json:"enabled"`
}

func (s *Server) listVendorProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListVendorProfiles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]vendorProfileDTO, 0, len(rows))
	for _, p := range rows {
		out = append(out, s.toVendorProfileDTO(r.Context(), p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createVendorProfile(w http.ResponseWriter, r *http.Request) {
	var req vendorProfileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.VendorType) == "" {
		http.Error(w, "name and vendor_type are required", http.StatusBadRequest)
		return
	}
	cfg, _ := json.Marshal(req.Config)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	p, err := s.queries.CreateVendorProfile(r.Context(), db.CreateVendorProfileParams{
		Name: req.Name, VendorType: req.VendorType, TargetUrl: req.TargetURL,
		CredentialID: parseUUIDPtr(&req.CredentialID), LocationID: parseUUIDPtr(&req.LocationID),
		DeviceID: parseUUIDPtr(&req.DeviceID), Config: cfgOrEmpty(cfg), Enabled: enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "credential", "vendor_profile.create", "vendor_profile", p.ID.String(), "Created vendor profile "+p.Name, map[string]any{"vendor_type": p.VendorType})
	writeJSON(w, http.StatusOK, s.toVendorProfileDTO(r.Context(), p))
}

func (s *Server) updateVendorProfile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req vendorProfileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	cfg, _ := json.Marshal(req.Config)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	p, err := s.queries.UpdateVendorProfile(r.Context(), db.UpdateVendorProfileParams{
		ID: id, Name: req.Name, VendorType: req.VendorType, TargetUrl: req.TargetURL,
		CredentialID: parseUUIDPtr(&req.CredentialID), LocationID: parseUUIDPtr(&req.LocationID),
		DeviceID: parseUUIDPtr(&req.DeviceID), Config: cfgOrEmpty(cfg), Enabled: enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toVendorProfileDTO(r.Context(), p))
}

func (s *Server) deleteVendorProfile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Read the profile first so we can clean up its auto-generated credential after.
	prof, perr := s.queries.GetVendorProfile(r.Context(), id)
	if err := s.queries.DeleteVendorProfile(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	// Orphan-credential cleanup: a credential auto-generated by the wireless
	// "Add controller" flow (name "… wireless admin @<ip>") that is no longer
	// referenced by any other profile is deleted (FK ON DELETE SET NULL unbinds it
	// from the device). Operator-managed / shared credentials are never touched.
	if perr == nil && prof.CredentialID != nil {
		if cred, cerr := s.queries.GetCredential(r.Context(), *prof.CredentialID); cerr == nil && strings.Contains(cred.Name, "wireless admin @") {
			if n, nerr := s.queries.CountVendorProfilesUsingCredential(r.Context(), prof.CredentialID); nerr == nil && n == 0 {
				_ = s.queries.DeleteCredential(r.Context(), *prof.CredentialID)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func cfgOrEmpty(b []byte) []byte {
	if len(b) == 0 || string(b) == "null" {
		return []byte("{}")
	}
	return b
}

// --- Test connection ---------------------------------------------------------

// testVendorProfile handles POST /vendor-profiles/{id}/test.
func (s *Server) testVendorProfile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	p, err := s.queries.GetVendorProfile(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	ok, detail := s.vendorProfileTest(r.Context(), p)
	_ = s.queries.SetVendorProfileTest(r.Context(), db.SetVendorProfileTestParams{ID: id, LastTestOk: &ok, LastTestDetail: detail})
	s.audit(r, "credential", "vendor_profile.test", "vendor_profile", id.String(), "Tested vendor profile "+p.Name, map[string]any{"ok": ok})
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "detail": detail})
}

// vendorProfileTest authenticates the profile against its target. Returns a
// non-secret outcome detail. Alcatel is an honest gate (no protocol yet).
func (s *Server) vendorProfileTest(ctx context.Context, p db.VendorConnectionProfile) (bool, string) {
	cfg := parseVPConfig(p.Config)
	user, pass, ok := s.vendorProfileSecret(ctx, p)
	needsCred := p.VendorType != "alcatel"
	if needsCred && !ok {
		return false, "no usable credential bound to this profile (or encryption key not loaded)"
	}
	base := strings.TrimRight(p.TargetUrl, "/")
	doer := insecureDoer(15 * time.Second)
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	switch p.VendorType {
	case "vmware":
		u, err := soap.ParseURL(vsphereSDKURL(base))
		if err != nil {
			return false, "invalid vCenter/ESXi URL"
		}
		u.User = url.UserPassword(user, pass)
		c, err := govmomi.NewClient(cctx, u, true)
		if err != nil {
			cat, d := categorizeCollectErr("vsphere", err.Error())
			_ = cat
			return false, "vSphere login failed: " + d
		}
		defer func() { _ = c.Logout(context.Background()) }()
		inv, _ := vsphere.Collect(cctx, c.Client)
		return true, "vSphere authenticated — " + itoaN(len(inv.Hosts)) + " host(s), " + itoaN(len(inv.VMs)) + " VM(s)"
	case "cctv":
		host := stripScheme(base)
		info, err := onvif.Collect(cctx, onvif.NewClient("http://"+host, user, pass, doer))
		if err != nil {
			_, d := categorizeCollectErr("onvif", err.Error())
			return false, "ONVIF failed: " + d
		}
		return true, "ONVIF authenticated — " + nz(info.Manufacturer, "?") + " " + nz(info.Model, "")
	case "wireless_unifi":
		c := unifi.NewClient(base, nz(cfg.Site, "default"), user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "UniFi login failed: " + shortErr(err)
		}
		aps, _ := c.ListAPs(cctx)
		return true, "UniFi authenticated — " + itoaN(len(aps)) + " AP(s)"
	case "wireless_omada":
		c := omada.NewClient(base, cfg.ControllerID, nz(cfg.Site, "Default"), user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "Omada login failed: " + shortErr(err)
		}
		aps, _ := c.ListAPs(cctx)
		return true, "Omada authenticated — " + itoaN(len(aps)) + " AP(s)"
	case "wireless_ruckus":
		c := ruckus.NewClient(base, cfg.APIBase, user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "Ruckus login failed: " + shortErr(err)
		}
		aps, _ := c.ListAPs(cctx)
		return true, "Ruckus authenticated — " + itoaN(len(aps)) + " AP(s)"
	case "wireless_extreme":
		c := extreme.NewClient(base, user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "Extreme login failed: " + shortErr(err)
		}
		aps, _ := c.ListAPs(cctx)
		return true, "Extreme authenticated — " + itoaN(len(aps)) + " AP(s)"
	case "extreme_xcc":
		// SAFE DISCOVERY (Phase 3): authenticate if possible + probe a small list
		// of read-only API endpoints, reporting status/content-type only — never
		// secrets or bodies. Saves the discovered API base into the profile config.
		return s.exploreXCC(ctx, p, cfg, base, user, pass)
	case "ruckus_zd":
		// ZoneDirector Web-XML: log in (admin-path discovery + CSRF) and fetch the
		// AP roster — exercises the full AJAX path as a fast connectivity check.
		zc := ruckuszd.New(base, user, pass, ruckusZDDoer(cfg, 15*time.Second))
		n, err := zc.Ping(cctx)
		if err != nil {
			return false, "Ruckus ZoneDirector login failed: " + shortErr(err)
		}
		return true, "Ruckus ZoneDirector authenticated (admin path /" + zc.AdminBase() + "/) — " + itoaN(n) + " AP(s)"
	case "wireless_aruba":
		return false, "Aruba controller integration not implemented yet — profile saved; detection + classification active, deep collection pending"
	case "cucm":
		c := cucm.NewClient(base, user, pass, cfg.Version, doer)
		phones, err := c.ListPhones(cctx)
		if err != nil {
			return false, "CUCM AXL failed: " + shortErr(err)
		}
		return true, "CUCM AXL authenticated — " + itoaN(len(phones)) + " phone(s)"
	case "alcatel":
		return false, "Alcatel OmniPCX/OmniVista integration not implemented yet — profile saved so the device is tracked with detection + classification + this honest gate; deep collection pending vendor API support"
	}
	return false, "unknown vendor_type: " + p.VendorType
}

// runVendorProfileCollection handles POST /vendor-profiles/{id}/run-collection.
// Runs deep collection for the profile's bound device via the matching collector
// and records the outcome. Site-level (no device) profiles are used during scan;
// a manual run needs a device binding.
func (s *Server) runVendorProfileCollection(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	p, err := s.queries.GetVendorProfile(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}

	// An optional device_id in the body lets the operator retry a site/global
	// profile against a specific scanned device (the "Retry with profile" action
	// in Scan Results). Otherwise we use the profile's bound device.
	var body struct {
		DeviceID string `json:"device_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	targetDevice := p.DeviceID
	if strings.TrimSpace(body.DeviceID) != "" {
		if did, perr := uuid.Parse(strings.TrimSpace(body.DeviceID)); perr == nil {
			targetDevice = &did
		}
	}
	if targetDevice == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"collected": false,
			"detail":    "this profile is site-level — it is used automatically during a scan. Bind it to a device (or retry from a Scan Result) to run an on-demand collection.",
		})
		return
	}
	dev, err := s.queries.GetDevice(ctx, *targetDevice)
	if err != nil {
		writeErr(w, err)
		return
	}

	ok, detail := false, ""
	switch p.VendorType {
	case "vmware":
		res := s.collectVSphereProfile(ctx, p, dev)
		ok, detail = res.CollectionOK, res.Detail
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: id, LastTestOk: &res.AuthOK, LastTestDetail: detail})
	case "cctv":
		res := s.collectCCTVProfile(ctx, p, dev)
		ok, detail = res.CollectionOK, res.Detail
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: id, LastTestOk: &res.AuthOK, LastTestDetail: detail})
	case "wireless_unifi", "wireless_omada", "wireless_ruckus", "wireless_extreme":
		ok, detail = s.collectWirelessProfile(ctx, p, dev)
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: id, LastTestOk: &ok, LastTestDetail: detail})
	case "extreme_xcc":
		ok, detail = s.collectXCCProfile(ctx, p, dev)
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: id, LastTestOk: &ok, LastTestDetail: detail})
	case "ruckus_zd":
		ok, detail = s.collectRuckusZDProfile(ctx, p, dev)
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: id, LastTestOk: &ok, LastTestDetail: detail})
	case "cucm":
		ok, detail = s.collectCUCMProfile(ctx, p, dev)
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: id, LastTestOk: &ok, LastTestDetail: detail})
	default:
		detail = p.VendorType + " deep collection not implemented yet — detection + classification + this gate remain active"
	}
	_ = s.queries.SetVendorProfileCollection(ctx, db.SetVendorProfileCollectionParams{ID: id, LastCollectionDetail: detail})
	if ok {
		s.audit(r, "inventory", "vendor_profile.run_collection", "vendor_profile", id.String(), "Ran vendor collection "+p.Name, map[string]any{"device": dev.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"collected": ok, "detail": detail})
}

// collectWirelessProfile logs into a wireless controller via the profile and
// persists controller + AP inventory onto the device.
func (s *Server) collectWirelessProfile(ctx context.Context, p db.VendorConnectionProfile, dev db.Device) (bool, string) {
	user, pass, hasCred := s.vendorProfileSecret(ctx, p)
	if !hasCred {
		return false, "no usable credential bound to this profile"
	}
	cfg := parseVPConfig(p.Config)
	base := strings.TrimRight(p.TargetUrl, "/")
	doer := insecureDoer(20 * time.Second)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var aps []wlanAP
	var vendor string
	switch p.VendorType {
	case "wireless_unifi":
		vendor = "Ubiquiti UniFi"
		c := unifi.NewClient(base, nz(cfg.Site, "default"), user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "UniFi login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(cctx)
		if err != nil {
			return false, "UniFi AP list failed: " + shortErr(err)
		}
		for _, a := range raw {
			aps = append(aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, a.ClientCount})
		}
	case "wireless_omada":
		vendor = "TP-Link Omada"
		c := omada.NewClient(base, cfg.ControllerID, nz(cfg.Site, "Default"), user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "Omada login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(cctx)
		if err != nil {
			return false, "Omada AP list failed: " + shortErr(err)
		}
		for _, a := range raw {
			aps = append(aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, a.ClientCount})
		}
	case "wireless_ruckus":
		vendor = "Ruckus"
		c := ruckus.NewClient(base, cfg.APIBase, user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "Ruckus login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(cctx)
		if err != nil {
			return false, "Ruckus AP list failed: " + shortErr(err)
		}
		for _, a := range raw {
			aps = append(aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, a.ClientCount})
		}
	case "wireless_extreme":
		vendor = "Extreme"
		c := extreme.NewClient(base, user, pass, doer)
		if err := c.Login(cctx); err != nil {
			return false, "Extreme login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(cctx)
		if err != nil {
			return false, "Extreme AP list failed: " + shortErr(err)
		}
		for _, a := range raw {
			aps = append(aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, a.ClientCount})
		}
	}

	ven := vendor
	_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
		DeviceID: dev.ID, Vendor: &ven, ApCount: int32(len(aps)),
	})
	for _, a := range aps {
		var mac, model *string
		if a.mac != "" {
			mac = &a.mac
		}
		if a.model != "" {
			model = &a.model
		}
		var ip *netip.Addr
		if addr, perr := netip.ParseAddr(a.ip); perr == nil {
			ip = &addr
		}
		st := a.status
		if st == "" {
			st = "unknown"
		}
		_, _ = s.queries.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
			ControllerDeviceID: dev.ID, Name: a.name, Mac: mac, Model: model, Ip: ip, Status: st, ClientCount: a.clients,
		})
	}
	if blob, merr := domain.MarshalEvidence(nil); merr == nil {
		conf := int16(85)
		dc := "wireless_controller"
		_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
			ID: dev.ID, Category: string(domain.CatWirelessController), OsFamily: domain.OSFamilyNetwork,
			DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
		})
	}
	_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{ID: dev.ID, Vendor: vendor})
	if p.CredentialID != nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: dev.ID, CredentialID: p.CredentialID})
	}
	_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: dev.ID, Status: "up"})
	return true, vendor + " controller collected — " + itoaN(len(aps)) + " AP(s)"
}

type wlanAP struct {
	name, mac, model, ip, status string
	clients                      int32
}

// collectCUCMProfile pulls the CUCM phone registry via the profile and persists it.
func (s *Server) collectCUCMProfile(ctx context.Context, p db.VendorConnectionProfile, dev db.Device) (bool, string) {
	user, pass, hasCred := s.vendorProfileSecret(ctx, p)
	if !hasCred {
		return false, "no usable credential bound to this profile"
	}
	cfg := parseVPConfig(p.Config)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := cucm.NewClient(strings.TrimRight(p.TargetUrl, "/"), user, pass, cfg.Version, insecureDoer(20*time.Second))
	phones, err := c.ListPhones(cctx)
	if err != nil {
		return false, "CUCM AXL failed: " + shortErr(err)
	}
	now := time.Now().UTC()
	for _, ph := range phones {
		var model, desc, pool *string
		if ph.Model != "" {
			model = &ph.Model
		}
		if ph.Description != "" {
			desc = &ph.Description
		}
		if ph.DevicePool != "" {
			pool = &ph.DevicePool
		}
		_ = s.queries.UpsertPbxPhone(ctx, db.UpsertPbxPhoneParams{
			DeviceID: dev.ID, Name: ph.Name, Model: model, Description: desc, DevicePool: pool,
			CollectionSource: "axl", LastSeenAt: now,
		})
	}
	if blob, merr := domain.MarshalEvidence(nil); merr == nil {
		conf := int16(85)
		dc := "cucm"
		_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
			ID: dev.ID, Category: string(domain.CatPBX), OsFamily: "", DeviceClass: &dc,
			ConfidenceScore: &conf, ClassificationEvidence: blob,
		})
	}
	if p.CredentialID != nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: dev.ID, CredentialID: p.CredentialID})
	}
	_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: dev.ID, Status: "up"})
	return true, "CUCM collected — " + itoaN(len(phones)) + " phone(s)"
}

// resolveScanProfile finds the best enabled Vendor Connection Profile for a
// device's classified category during a scan (most specific: device > site >
// global). Returns false when none is configured (the honest gate).
func (s *Server) resolveScanProfile(ctx context.Context, category string, devID uuid.UUID, locID *uuid.UUID) (db.VendorConnectionProfile, bool) {
	var vendorTypes []string
	switch category {
	case string(domain.CatVirtualHost):
		vendorTypes = []string{"vmware"}
	case string(domain.CatCamera), string(domain.CatNVR):
		vendorTypes = []string{"cctv"}
	case string(domain.CatWirelessController), string(domain.CatAccessPoint):
		vendorTypes = []string{"extreme_xcc", "ruckus_zd", "wireless_unifi", "wireless_omada", "wireless_ruckus", "wireless_extreme", "wireless_aruba"}
	case string(domain.CatPBX), string(domain.CatVoiceGateway), string(domain.CatIPPhone):
		vendorTypes = []string{"cucm", "alcatel"}
	}
	for _, vt := range vendorTypes {
		profs, err := s.queries.ResolveVendorProfiles(ctx, db.ResolveVendorProfilesParams{
			VendorType: vt, DeviceID: &devID, LocationID: locID,
		})
		if err == nil && len(profs) > 0 {
			return profs[0], true
		}
	}
	return db.VendorConnectionProfile{}, false
}

// vendorProfileSecret decrypts the profile's bound credential into user/pass.
func (s *Server) vendorProfileSecret(ctx context.Context, p db.VendorConnectionProfile) (string, string, bool) {
	u, pw, _, _, ok := s.vendorProfileCred(ctx, p)
	return u, pw, ok
}

// vendorProfileCred is vendorProfileSecret plus the credential id + kind, used by
// profile-driven scan collection to record an accurate Credential Test History
// attempt (which credential, which kind) on success/failure. Secrets stay in-memory.
func (s *Server) vendorProfileCred(ctx context.Context, p db.VendorConnectionProfile) (user, pass string, credID uuid.UUID, kind domain.CredentialKind, ok bool) {
	if p.CredentialID == nil {
		return "", "", uuid.Nil, "", false
	}
	cph := s.cipher()
	if cph == nil {
		return "", "", uuid.Nil, "", false
	}
	c, err := s.queries.GetCredential(ctx, *p.CredentialID)
	if err != nil {
		return "", "", uuid.Nil, "", false
	}
	plain, err := cph.Open(c.EncryptedBlob, c.KeyID)
	if err != nil {
		return "", "", uuid.Nil, "", false
	}
	u, pw := credtest.SplitUserPass(string(plain))
	return u, pw, c.ID, domain.CredentialKind(c.Kind), true
}

func vsphereSDKURL(base string) string {
	if base == "" {
		return base
	}
	if !strings.HasPrefix(base, "http") {
		base = "https://" + base
	}
	if !strings.Contains(base, "/sdk") {
		base = strings.TrimRight(base, "/") + "/sdk"
	}
	return base
}

func stripScheme(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimRight(u, "/")
}

func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func shortErr(err error) string {
	m := err.Error()
	if len(m) > 120 {
		m = m[:120] + "…"
	}
	return m
}

func itoaN(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
