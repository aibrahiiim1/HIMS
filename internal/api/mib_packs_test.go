package api

import "testing"

// TestMibMatchScore_VendorExactWins is the cross-vendor guard: a vendor-exact
// sysObjectID match (score 3) must outrank a broad category match (score 1), so
// the HiPath pack (which claims all wireless_controllers via category) cannot
// shadow the vendor-exact Ruckus pack on a Ruckus device — and vice-versa.
func TestMibMatchScore_VendorExactWins(t *testing.T) {
	hipath := mibAppliesTo{
		SysObjectIDPrefixes: []string{"1.3.6.1.4.1.1916", "1.3.6.1.4.1.5624"},
		SysDescrContains:    []string{"ExtremeCloud IQ Controller", "Summit WM", "HiPath Wireless"},
		Categories:          []string{"wireless_controller"},
	}
	ruckus := mibAppliesTo{
		SysObjectIDPrefixes: []string{"1.3.6.1.4.1.25053"},
		SysDescrContains:    []string{"ruckus", "zonedirector", "zd3"},
	}

	// Ruckus ZD3050: Ruckus pack matches by OID (3), HiPath only by category (1).
	if s := mibMatchScore(ruckus, ".1.3.6.1.4.1.25053.3.1.5.3", "Ruckus Wireless zd3050", "wireless_controller"); s != 3 {
		t.Errorf("ruckus pack vs ruckus device = %d, want 3 (oid)", s)
	}
	if s := mibMatchScore(hipath, ".1.3.6.1.4.1.25053.3.1.5.3", "Ruckus Wireless zd3050", "wireless_controller"); s != 1 {
		t.Errorf("hipath pack vs ruckus device = %d, want 1 (category only)", s)
	}

	// Extreme VE6120: HiPath matches by OID (3), Ruckus not at all (0 — no broad category).
	if s := mibMatchScore(hipath, ".1.3.6.1.4.1.1916.2.284", "ExtremeCloud IQ Controller", "wireless_controller"); s != 3 {
		t.Errorf("hipath vs extreme = %d, want 3 (oid)", s)
	}
	if s := mibMatchScore(ruckus, ".1.3.6.1.4.1.1916.2.284", "ExtremeCloud IQ Controller", "wireless_controller"); s != 0 {
		t.Errorf("ruckus vs extreme = %d, want 0", s)
	}
}

// TestBuiltinRuckusZDTables pins the RUCKUS-ZD-WLAN-MIB roots + the column subIDs
// that map the AP/WLAN/station tables (verified against the MIB).
func TestBuiltinRuckusZDTables(t *testing.T) {
	byPurpose := map[string]builtinTable{}
	for _, tb := range builtinRuckusZDTables() {
		switch tb.purpose {
		case "aps", "ssids", "clients":
			byPurpose[tb.purpose] = tb
		}
	}
	ap, ok := byPurpose["aps"]
	if !ok {
		t.Fatal("ruckus pack has no aps table")
	}
	if ap.oid != "1.3.6.1.4.1.25053.1.2.2.1.1.2.1" {
		t.Errorf("ap table root = %q, want ruckusZDWLANAPTable", ap.oid)
	}
	if ap.cols["ap_serial"] != 5 || ap.cols["ap_status"] != 3 || ap.cols["ap_client_count"] != 15 {
		t.Errorf("ap column map wrong: %v", ap.cols)
	}
	if c, ok := byPurpose["clients"]; !ok || c.cols["client_mac"] != 1 || c.cols["client_ap"] != 2 {
		t.Errorf("client column map wrong: %v", byPurpose["clients"])
	}
	if s, ok := byPurpose["ssids"]; !ok || s.cols["ssid_ssid"] != 1 || s.oid != "1.3.6.1.4.1.25053.1.2.2.1.1.1.1" {
		t.Errorf("ssid table wrong: %+v", byPurpose["ssids"])
	}
}
