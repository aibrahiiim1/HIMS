package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// addWirelessControllerReq is the one-step "Inventory → Wireless → Add controller"
// payload: IP + vendor + admin credential. The password is sealed into a
// credentials row (never stored plaintext); a device + enabled vendor profile are
// created and an immediate REST/XML collection is kicked.
type addWirelessControllerReq struct {
	Vendor     string  `json:"vendor"` // extreme_xcc | ruckus_zd
	IP         string  `json:"ip"`
	Name       string  `json:"name"`
	LocationID *string `json:"location_id"`
	Username   string  `json:"username"`
	Password   string  `json:"password"`
	Port       int     `json:"port"`     // optional (default 5825 extreme / 443 ruckus_zd)
	APIBase    string  `json:"api_base"` // optional (extreme; default /management/v1)
	SSLVerify  bool    `json:"ssl_verify"`
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
	if req.Vendor != "extreme_xcc" && req.Vendor != "ruckus_zd" {
		http.Error(w, "vendor must be 'extreme_xcc' or 'ruckus_zd'", http.StatusBadRequest)
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(req.IP))
	if err != nil {
		http.Error(w, "invalid ip", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}
	port := req.Port
	if port <= 0 {
		if req.Vendor == "extreme_xcc" {
			port = 5825
		} else {
			port = 443
		}
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
	cred, err := s.queries.CreateCredential(ctx, db.CreateCredentialParams{
		Name: name + " (wireless admin)", Kind: string(domain.CredHTTPBasic),
		EncryptedBlob: blob, KeyID: keyID,
		Weak: isWeakSecret(string(domain.CredHTTPBasic), req.Password), Metadata: []byte("{}"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	// 2. Find-or-create the controller device by (ip, location).
	dev, err := s.queries.LiveDeviceByIPAndLocation(ctx, db.LiveDeviceByIPAndLocationParams{PrimaryIp: &ip, LocationID: locID})
	if err != nil {
		dev, err = s.queries.CreateDevice(ctx, db.CreateDeviceParams{
			LocationID: locID, PrimaryIp: &ip, Name: name, Category: string(domain.CatWirelessController),
			Status: "unknown", Metadata: []byte(`{"source":"wireless_controller_add"}`),
		})
		if err != nil {
			writeErr(w, err)
			return
		}
	}

	// 3. Create the enabled vendor profile bound to the device (the PRIMARY path).
	cfg := map[string]any{"ssl_verify": req.SSLVerify}
	if req.Vendor == "extreme_xcc" {
		base := strings.TrimSpace(req.APIBase)
		if base == "" {
			base = "/management/v1"
		}
		cfg["api_base"] = base
	}
	cfgJSON, _ := json.Marshal(cfg)
	targetURL := "https://" + ip.String() + ":" + strconv.Itoa(port)
	devID := dev.ID
	credID := cred.ID
	prof, err := s.queries.CreateVendorProfile(ctx, db.CreateVendorProfileParams{
		Name: name, VendorType: req.Vendor, TargetUrl: targetURL,
		CredentialID: &credID, LocationID: locID, DeviceID: &devID,
		Config: cfgJSON, Enabled: true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "inventory", "wireless.add_controller", "device", dev.ID.String(),
		"Added "+req.Vendor+" wireless controller "+name, map[string]any{"vendor": req.Vendor, "ip": ip.String()})

	// 4. Kick the REST/XML collection asynchronously — the primary collection path.
	go func(p db.VendorConnectionProfile, d db.Device) {
		bg, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_, _, _ = s.collectWirelessForDevice(bg, d, &p)
	}(prof, dev)

	src := xccSource
	if req.Vendor == "ruckus_zd" {
		src = ruckusZDSource
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"device_id":  dev.ID.String(),
		"profile_id": prof.ID.String(),
		"source":     src,
		"detail":     "Controller added; REST/XML collection started (primary). It will appear under Inventory → Wireless shortly.",
	})
}

// collectWirelessForDevice is the single authority for wireless collection
// precedence (runbook §4.5): if an enabled extreme_xcc/ruckus_zd vendor profile is
// bound to the device, it runs that REST/XML collector (PRIMARY) and returns
// handled=true; otherwise it returns handled=false so the caller falls back to the
// existing SNMP/SSH MIB collection (unchanged). Call it from the manual "collect
// now" action and the add-controller flow so precedence can never drift. Pass a
// known profile to skip the lookup (the add-controller flow already has it).
func (s *Server) collectWirelessForDevice(ctx context.Context, dev db.Device, known *db.VendorConnectionProfile) (handled, ok bool, detail string) {
	run := func(p db.VendorConnectionProfile) (bool, string) {
		var rok bool
		var rdetail string
		switch p.VendorType {
		case "extreme_xcc":
			rok, rdetail = s.collectXCCProfile(ctx, p, dev)
		case "ruckus_zd":
			rok, rdetail = s.collectRuckusZDProfile(ctx, p, dev)
		default:
			return false, ""
		}
		_ = s.queries.SetVendorProfileTest(ctx, db.SetVendorProfileTestParams{ID: p.ID, LastTestOk: &rok, LastTestDetail: rdetail})
		_ = s.queries.SetVendorProfileCollection(ctx, db.SetVendorProfileCollectionParams{ID: p.ID, LastCollectionDetail: rdetail})
		return rok, rdetail
	}

	if known != nil && known.Enabled && (known.VendorType == "extreme_xcc" || known.VendorType == "ruckus_zd") {
		ok, detail = run(*known)
		return true, ok, detail
	}
	for _, vt := range []string{"extreme_xcc", "ruckus_zd"} {
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
