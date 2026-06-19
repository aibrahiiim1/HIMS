package api

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// isWirelessController reports whether a device is a wireless controller/AP.
func isWirelessController(d db.Device) bool {
	return d.Category == string(domain.CatWirelessController) || d.Category == string(domain.CatAccessPoint)
}

// wirelessVendorType maps a wireless controller's vendor to the profile/collector
// vendor_type. Returns "" when the vendor can't be determined (the operator then
// picks one). On-prem Extreme → extreme_xcc, Ruckus → ruckus_zd (ZoneDirector,
// the deployed form), UniFi/Omada → their REST profiles.
func wirelessVendorType(d db.Device) string {
	v := ""
	if d.Vendor != nil {
		v = strings.ToLower(*d.Vendor)
	}
	m := ""
	if d.Model != nil {
		m = strings.ToLower(*d.Model)
	}
	hay := v + " " + m
	switch {
	case strings.Contains(hay, "extreme"), strings.Contains(hay, "aerohive"):
		return "extreme_xcc"
	case strings.Contains(hay, "ruckus"):
		return "ruckus_zd"
	case strings.Contains(hay, "ubiquiti"), strings.Contains(hay, "unifi"):
		return "wireless_unifi"
	case strings.Contains(hay, "tp-link"), strings.Contains(hay, "tplink"), strings.Contains(hay, "omada"):
		return "wireless_omada"
	}
	return ""
}

// defaultWirelessURL is the management URL HIMS pre-fills for a vendor when
// auto-creating a profile from the discovered IP.
func defaultWirelessURL(vt, ip string) string {
	switch vt {
	case "extreme_xcc":
		return "https://" + ip + ":5825"
	case "wireless_unifi":
		return "https://" + ip + ":8443"
	case "wireless_omada":
		return "https://" + ip + ":8043"
	default: // ruckus_zd / wireless_ruckus / wireless_extreme
		return "https://" + ip
	}
}

// wirelessCredCandidates returns login-kind credential ids to try for a wireless
// controller, subnet-scoped first (anti-spray: an IP inside a scoped subnet uses
// ONLY its assigned credentials), then the device's bound credential, then —
// only if the resolver yielded nothing — stored http_basic/vendor_api creds.
// Capped; first success wins so nothing is sprayed needlessly.
func (s *Server) wirelessCredCandidates(ctx context.Context, dev db.Device) []uuid.UUID {
	const cap = 6
	loginKind := func(k string) bool {
		switch k {
		case "http_basic", "vendor_api", "onvif", "ssh":
			return true
		}
		return false
	}
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(id uuid.UUID) {
		if id != uuid.Nil && !seen[id] && len(out) < cap {
			seen[id] = true
			out = append(out, id)
		}
	}
	// Device's bound credential first (operator intent).
	if dev.CredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *dev.CredentialID); err == nil && loginKind(c.Kind) {
			add(c.ID)
		}
	}
	// Subnet-scoped resolver candidates (respects exclusive subnet scope).
	scoped := false
	if s.fetcher != nil && dev.PrimaryIp != nil {
		if groups, err := s.fetcher.CredentialCandidates(ctx, *dev.PrimaryIp, dev.LocationID); err == nil {
			for _, g := range groups {
				for _, m := range g.Members {
					if loginKind(string(m.Kind)) {
						scoped = true
						add(m.ID)
					}
				}
			}
		}
	}
	// Global fallback ONLY when the resolver produced nothing (no scoped creds) —
	// so a scoped subnet is never sprayed with out-of-scope credentials. Order by
	// relevance so the obviously-matching credential (named for this IP / vendor /
	// "wireless") is tried first and the cap doesn't miss it.
	if !scoped {
		ip := dev.PrimaryIp.String()
		ven := ""
		if dev.Vendor != nil {
			ven = strings.ToLower(*dev.Vendor)
		}
		score := func(name string) int {
			n := strings.ToLower(name)
			switch {
			case strings.Contains(n, ip):
				return 3
			case ven != "" && strings.Contains(n, strings.Fields(ven)[0]):
				return 2
			case strings.Contains(n, "wireless"), strings.Contains(n, "wifi"), strings.Contains(n, "wlan"),
				strings.Contains(n, "ruckus"), strings.Contains(n, "extreme"), strings.Contains(n, "zd"), strings.Contains(n, "xiq"), strings.Contains(n, "unifi"), strings.Contains(n, "omada"):
				return 1
			}
			return 0
		}
		if rows, err := s.queries.ListCredentials(ctx); err == nil {
			type sc struct {
				id  uuid.UUID
				pri int
			}
			var ranked []sc
			for _, c := range rows {
				if c.Kind == "http_basic" || c.Kind == "vendor_api" {
					ranked = append(ranked, sc{c.ID, score(c.Name)})
				}
			}
			sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].pri > ranked[j].pri })
			for _, r := range ranked {
				add(r.id)
			}
		}
	}
	return out
}

// ensureWirelessProfile finds the device's existing (device, vendor_type) profile
// or auto-creates one from the discovered IP — idempotent, so repeated scans
// reuse the same profile instead of duplicating. Returns (profile, created).
func (s *Server) ensureWirelessProfile(ctx context.Context, dev db.Device, vt string, firstCred *uuid.UUID) (db.VendorConnectionProfile, bool, error) {
	if existing, err := s.queries.GetVendorProfileForDeviceVendor(ctx, db.GetVendorProfileForDeviceVendorParams{DeviceID: &dev.ID, VendorType: vt}); err == nil {
		return existing, false, nil
	}
	devID := dev.ID
	p, err := s.queries.CreateVendorProfile(ctx, db.CreateVendorProfileParams{
		Name:         dev.Name + " (" + vt + ")",
		VendorType:   vt,
		TargetUrl:    defaultWirelessURL(vt, dev.PrimaryIp.String()),
		CredentialID: firstCred,
		LocationID:   dev.LocationID,
		DeviceID:     &devID,
		Config:       []byte(`{"insecure":true}`),
		Enabled:      true,
	})
	return p, err == nil, err
}

// runWirelessProfileDispatch runs the right on-prem collector for the vendor_type.
// XCC needs its API path discovered first (exploreXCC saves it to the profile),
// so we discover-then-collect transparently.
func (s *Server) runWirelessProfileDispatch(ctx context.Context, vt string, prof db.VendorConnectionProfile, dev db.Device) (bool, string) {
	switch vt {
	case "extreme_xcc":
		cfg := parseVPConfig(prof.Config)
		if strings.TrimSpace(cfg.APIBase) == "" {
			user, pass, ok := s.vendorProfileSecret(ctx, prof)
			if !ok {
				return false, "no usable credential bound to this profile"
			}
			base := strings.TrimRight(prof.TargetUrl, "/")
			if eok, edetail := s.exploreXCC(ctx, prof, cfg, base, user, pass); !eok {
				return false, edetail
			}
			if p2, err := s.queries.GetVendorProfile(ctx, prof.ID); err == nil {
				prof = p2 // pick up the discovered api_base saved by exploreXCC
			}
		}
		return s.collectXCCProfile(ctx, prof, dev)
	case "ruckus_zd":
		return s.collectRuckusZDProfile(ctx, prof, dev)
	default: // wireless_unifi / wireless_omada / wireless_ruckus / wireless_extreme
		return s.collectWirelessProfile(ctx, prof, dev)
	}
}

// collectWirelessAuto is the profile-free wireless deep-collection path: identify
// the vendor, auto-create (or reuse) the right profile from the discovered IP,
// try subnet-scoped login credentials until one authenticates, run the on-prem
// collector (AP / SSID / client roster), and persist the winning credential on
// the profile for future scheduled runs. The operator never authors a profile.
func (s *Server) collectWirelessAuto(ctx context.Context, dev db.Device) map[string]any {
	vt := wirelessVendorType(dev)
	if vt == "" {
		return map[string]any{"collected": false, "state": "config_required", "kind": "wireless",
			"detail": "wireless controller detected but its vendor is unknown — open the device and choose Extreme / Ruckus / UniFi / Omada to collect AP/SSID/client data."}
	}
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		return map[string]any{"collected": false, "state": "deep_inventory_failed", "kind": vt, "detail": "device has no IP to collect from"}
	}
	cands := s.wirelessCredCandidates(ctx, dev)
	if len(cands) == 0 {
		out := s.collectFailure(ctx, "http_basic", "no http_basic / vendor_api login credential is available for this wireless controller")
		out["kind"] = vt
		out["state"] = "credential_kind_mismatch"
		return out
	}

	var firstCred *uuid.UUID
	if len(cands) > 0 {
		firstCred = &cands[0]
	}
	prof, created, err := s.ensureWirelessProfile(ctx, dev, vt, firstCred)
	if err != nil {
		return map[string]any{"collected": false, "state": "deep_inventory_failed", "kind": vt, "detail": "could not prepare wireless profile: " + shortErr(err)}
	}

	lastDetail := "wireless collection failed"
	for _, cid := range cands {
		cidCopy := cid
		prof.CredentialID = &cidCopy // mutate the local copy; collectors read p.CredentialID
		ok, detail := s.runWirelessProfileDispatch(ctx, vt, prof, dev)
		lastDetail = detail
		if ok {
			// Persist the winning credential on the (reused/auto-created) profile so
			// future scheduled runs reuse it — no duplicate profile, no re-discovery.
			_, _ = s.queries.UpdateVendorProfile(ctx, db.UpdateVendorProfileParams{
				ID: prof.ID, Name: prof.Name, VendorType: prof.VendorType, TargetUrl: prof.TargetUrl,
				CredentialID: &cidCopy, LocationID: prof.LocationID, DeviceID: prof.DeviceID,
				Config: prof.Config, Enabled: true,
			})
			ap, ssid, client := s.wirelessCounts(ctx, dev.ID)
			return map[string]any{
				"collected": true, "state": "managed_direct", "kind": vt, "detail": detail,
				"profile_id": prof.ID.String(), "profile_created": created,
				"aps": ap, "ssids": ssid, "clients": client,
			}
		}
	}
	// All candidates failed — classify the failure (auth vs transport vs config).
	out := s.collectFailure(ctx, vt, lastDetail)
	out["kind"] = vt
	out["profile_id"] = prof.ID.String()
	out["profile_created"] = created
	return out
}

// wirelessCounts reads the persisted AP / SSID / client counts for a controller.
func (s *Server) wirelessCounts(ctx context.Context, devID uuid.UUID) (ap, ssid, client int) {
	if s.pool == nil {
		return 0, 0, 0
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = s.pool.QueryRow(cctx,
		`SELECT COALESCE(ap_count,0), COALESCE(ssid_count,0), COALESCE(client_count,0)
		   FROM wlan_controller_info WHERE device_id=$1`, devID).Scan(&ap, &ssid, &client)
	return ap, ssid, client
}
