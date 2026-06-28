package fingerprint

import "testing"

func TestOIDPrefixMatch(t *testing.T) {
	lib := Library()
	// A Cisco Catalyst sysObjectID with leading dot must match the Cisco PEN.
	res := Match(Evidence{SysObjectID: ".1.3.6.1.4.1.9.1.516"}, lib)
	if len(res) == 0 || res[0].Vendor != "Cisco" {
		t.Fatalf("expected Cisco as top match, got %+v", res)
	}
	// 1.3.6.1.4.1.99 must NOT match the 1.3.6.1.4.1.9 prefix (boundary check).
	for _, r := range Match(Evidence{SysObjectID: "1.3.6.1.4.1.99.1"}, lib) {
		if r.Pattern == "1.3.6.1.4.1.9" {
			t.Error("1.3.6.1.4.1.9 should not match enterprise 99 on a non-dotted boundary")
		}
	}
}

func TestExclusion_HPJetDirectNotSwitch(t *testing.T) {
	lib := Library()
	// HP JetDirect printer: OID under the .11 enterprise prefix AND the JetDirect
	// subtree .11.2.3.9 with an "ethernet multi-environment" sysDescr. The broad
	// .11→switch rule must be EXCLUDED; the .11.2.3.9→printer rule wins.
	ev := Evidence{SysObjectID: "1.3.6.1.4.1.11.2.3.9.1", SysDescr: "HP ETHERNET MULTI-ENVIRONMENT"}
	winners, rejected := MatchWithRejected(ev, lib)
	if len(winners) == 0 || winners[0].DeviceType != "printer" {
		t.Fatalf("HP JetDirect should classify as printer, got winners=%+v", winners)
	}
	for _, w := range winners {
		if w.DeviceType == "switch" {
			t.Errorf("no switch candidate should survive for an HP printer: %+v", w)
		}
	}
	// The excluded switch rule must appear in rejected with an exclusion reason.
	var sawExcluded bool
	for _, rj := range rejected {
		if rj.DeviceType == "switch" && rj.Pattern == "1.3.6.1.4.1.11" {
			sawExcluded = true
			if !contains(rj.Reason, "excluded") {
				t.Errorf("switch rejection reason should mention exclusion: %q", rj.Reason)
			}
		}
	}
	if !sawExcluded {
		t.Errorf("expected the .11 switch rule to be a rejected candidate, rejected=%+v", rejected)
	}
}

func TestExclusion_RealProCurveSwitchStillMatches(t *testing.T) {
	lib := Library()
	// A real HP ProCurve switch (.11.2.3.7 subtree, no printer markers) must STILL
	// match the .11→switch rule — the exclusion only fires on printer markers.
	ev := Evidence{SysObjectID: "1.3.6.1.4.1.11.2.3.7.11.180", SysDescr: "ProCurve J9..."}
	res := Match(ev, lib)
	var sawSwitch bool
	for _, r := range res {
		if r.DeviceType == "switch" && r.Pattern == "1.3.6.1.4.1.11" {
			sawSwitch = true
		}
	}
	if !sawSwitch {
		t.Fatalf("real ProCurve switch must still match the .11 switch rule, got %+v", res)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestServiceAndConfidenceRanking(t *testing.T) {
	lib := Library()
	// FortiGate sysDescr should resolve to Fortinet/firewall.
	res := Match(Evidence{SysDescr: "FortiGate-100F v7.2.5 build1517"}, lib)
	if len(res) == 0 || res[0].Vendor != "Fortinet" || res[0].DeviceType != "firewall" {
		t.Fatalf("expected Fortinet/firewall, got %+v", res)
	}
}

func TestHTTPAndSSHAndPort(t *testing.T) {
	lib := Library()
	if r := Match(Evidence{HTTPServer: "App-webs/"}, lib); len(r) == 0 || r[0].Vendor != "Hikvision" {
		t.Errorf("expected Hikvision from App-webs banner, got %+v", r)
	}
	if r := Match(Evidence{SSHBanner: "SSH-2.0-ROSSSH"}, lib); len(r) == 0 || r[0].Vendor != "MikroTik" {
		t.Errorf("expected MikroTik from ROSSSH banner, got %+v", r)
	}
	if r := Match(Evidence{Ports: []int{9100}}, lib); len(r) == 0 || r[0].DeviceType != "printer" {
		t.Errorf("expected printer from port 9100, got %+v", r)
	}
}

func TestNoEvidenceNoMatch(t *testing.T) {
	if r := Match(Evidence{}, Library()); len(r) != 0 {
		t.Errorf("empty evidence should yield no matches, got %d", len(r))
	}
}

func TestExtremeCloudControllerBeatsGenericSwitch(t *testing.T) {
	lib := Library()
	// The exact VE6120 sysObjectID (.1916.2.284) ALSO matches the generic Extreme
	// PEN prefix (.1916 → switch @82). The product fingerprint (@95) must win, so
	// 172.21.96.100 classifies as a wireless_controller, not a switch.
	ev := Evidence{
		SysObjectID: "1.3.6.1.4.1.1916.2.284",
		SysDescr:    "Extreme Networks ExtremeCloud IQ Controller - VE6120 Medium, System Version 10.05.04.0006",
		SysName:     "XIQC.coralsearesorts.com",
	}
	res := Match(ev, lib)
	if len(res) == 0 {
		t.Fatal("expected matches for VE6120 evidence")
	}
	top := res[0]
	if top.Kind != KindOID || top.Pattern != "1.3.6.1.4.1.1916.2.284" {
		t.Fatalf("expected exact VE6120 OID to rank first, got %+v", top)
	}
	if top.Vendor != "Extreme Networks" || top.DeviceType != "wireless_controller" {
		t.Fatalf("expected Extreme Networks/wireless_controller, got %+v", top)
	}
	if top.Confidence < 90 {
		t.Fatalf("expected exact-OID confidence ≥90 to beat generic switch @82, got %d", top.Confidence)
	}
	// The generic .1916 switch prefix is still present (for real Extreme switches)
	// but must rank BELOW the product print.
	var sawGenericSwitch bool
	for _, r := range res {
		if r.Pattern == "1.3.6.1.4.1.1916" && r.DeviceType == "switch" {
			sawGenericSwitch = true
			if r.Confidence >= top.Confidence {
				t.Errorf("generic Extreme switch prefix should not outrank the product print: %+v", r)
			}
		}
	}
	if !sawGenericSwitch {
		t.Error("expected the generic .1916 Extreme switch prefix to still be in the library")
	}
}

func TestExtremeCloudBySysDescrAlone(t *testing.T) {
	// Even without the sysObjectID (e.g. a device that only answers sysDescr), the
	// "ExtremeCloud IQ Controller" service print classifies it as wireless_controller.
	res := Match(Evidence{SysDescr: "ExtremeCloud IQ Controller - VE6120 Medium"}, Library())
	if len(res) == 0 || res[0].DeviceType != "wireless_controller" || res[0].Vendor != "Extreme Networks" {
		t.Fatalf("expected Extreme Networks/wireless_controller from sysDescr, got %+v", res)
	}
}

func TestRuckusZoneDirectorFingerprint(t *testing.T) {
	// Live evidence from a ZD3050: the product OID print must win and pin
	// vendor + wireless_controller + the explicit model (≥85 so reclassify applies
	// vendor/model). The generic 25053 PEN print is still present but ranks below.
	ev := Evidence{
		SysObjectID: ".1.3.6.1.4.1.25053.3.1.5.3",
		SysDescr:    "Ruckus Wireless zd3050",
		SysName:     "CSHV-ZD",
	}
	res := Match(ev, Library())
	if len(res) == 0 {
		t.Fatal("expected matches for Ruckus ZD evidence")
	}
	top := res[0]
	if top.Vendor != "Ruckus Wireless" || top.DeviceType != "wireless_controller" {
		t.Fatalf("expected Ruckus Wireless / wireless_controller, got %+v", top)
	}
	if top.Model != "ZoneDirector 3050" {
		t.Errorf("expected model ZoneDirector 3050, got %q", top.Model)
	}
	if top.Confidence < 85 {
		t.Errorf("expected high confidence (>=85) so vendor/model apply, got %d", top.Confidence)
	}
	// sysDescr-only fallback (no OID) still resolves to Ruckus Wireless controller.
	r2 := Match(Evidence{SysDescr: "Ruckus Wireless zd3050"}, Library())
	if len(r2) == 0 || r2[0].Vendor != "Ruckus Wireless" || r2[0].DeviceType != "wireless_controller" {
		t.Fatalf("expected Ruckus Wireless/wireless_controller from sysDescr alone, got %+v", r2)
	}
}

func TestModelFromSysDescr(t *testing.T) {
	cases := map[string]string{
		"Extreme Networks ExtremeCloud IQ Controller - VE6120 Medium, System Version 10.05.04.0006": "VE6120 Medium",
		"Some Vendor Product - X1000, v2": "X1000",
		"No model here":                   "",
		"Trailing - ":                     "",
	}
	for in, want := range cases {
		if got := ModelFromSysDescr(in); got != want {
			t.Errorf("ModelFromSysDescr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalCategory(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"wireless":            "wireless_controller",
		"voip":                "pbx",
		"switch":              "switch",
		"wireless_controller": "wireless_controller",
	}
	for in, want := range cases {
		if got := CanonicalCategory(in); got != want {
			t.Errorf("CanonicalCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSysNameMatch(t *testing.T) {
	lib := []Print{{Kind: KindSysName, Pattern: "XIQC", Vendor: "Extreme Networks", DeviceType: "wireless_controller", Confidence: 70}}
	if r := Match(Evidence{SysName: "XIQC.coralsearesorts.com"}, lib); len(r) == 0 || r[0].Kind != KindSysName {
		t.Fatalf("expected a sysName match, got %+v", r)
	}
	if r := Match(Evidence{SysDescr: "XIQC"}, lib); len(r) != 0 {
		t.Errorf("sysName print must not match against sysDescr, got %+v", r)
	}
}

func TestExtendedCatalog(t *testing.T) {
	lib := Library()
	cases := []struct {
		ev         Evidence
		wantVendor string
		wantType   string
	}{
		{Evidence{SysObjectID: "1.3.6.1.4.1.25053.1.2"}, "Ruckus Wireless", "wireless"},
		{Evidence{SysObjectID: "1.3.6.1.4.1.534.10"}, "Eaton", "ups"},
		{Evidence{SysObjectID: "1.3.6.1.4.1.24681.1"}, "QNAP", "storage"},         // Phase 4 SC2: NAS reclassified server→storage
		{Evidence{SysObjectID: "1.3.6.1.4.1.21342.3"}, "Grandstream", "ip_phone"}, // Phase 4 SC3: voip→ip_phone
		{Evidence{SysDescr: "Ruckus ZoneDirector 1200"}, "Ruckus Wireless", "wireless_controller"},
		{Evidence{SysDescr: "Alcatel-Lucent OmniSwitch 6450"}, "Alcatel-Lucent Enterprise", "switch"},
		{Evidence{SysDescr: "Yealink SIP-T46G"}, "Yealink", "ip_phone"}, // Phase 4 SC3: voip→ip_phone
	}
	for _, c := range cases {
		res := Match(c.ev, lib)
		if len(res) == 0 {
			t.Errorf("%+v: no match", c.ev)
			continue
		}
		if res[0].Vendor != c.wantVendor || res[0].DeviceType != c.wantType {
			t.Errorf("%+v: got %s/%s, want %s/%s", c.ev, res[0].Vendor, res[0].DeviceType, c.wantVendor, c.wantType)
		}
	}
}

func TestExplicitModelFlowsThroughMatch(t *testing.T) {
	// A rule with an explicit Model surfaces that model on the Result; a rule
	// without one leaves Result.Model empty (caller falls back to sysDescr).
	lib := []Print{
		{Kind: KindOID, Pattern: "1.3.6.1.4.1.9999.1", Vendor: "Acme", DeviceType: "router", Confidence: 90, Model: "ACME-9000"},
		{Kind: KindService, Pattern: "GenericThing", Vendor: "Gen", DeviceType: "server", Confidence: 70},
	}
	if r := Match(Evidence{SysObjectID: "1.3.6.1.4.1.9999.1.2"}, lib); len(r) == 0 || r[0].Model != "ACME-9000" {
		t.Fatalf("expected explicit model ACME-9000, got %+v", r)
	}
	if r := Match(Evidence{SysDescr: "GenericThing v1"}, lib); len(r) == 0 || r[0].Model != "" {
		t.Fatalf("expected empty model for model-less rule, got %+v", r)
	}
}

func TestMultiSignalRanksStrongest(t *testing.T) {
	lib := Library()
	// Both an OID (conf 82) and a generic OpenSSH banner (conf 30) present:
	// the OID must win.
	res := Match(Evidence{SysObjectID: "1.3.6.1.4.1.9.1.1", SSHBanner: "SSH-2.0-OpenSSH_8.0"}, lib)
	if len(res) < 2 {
		t.Fatalf("expected at least 2 matches, got %+v", res)
	}
	if res[0].Kind != KindOID {
		t.Errorf("OID match should rank first, got %+v", res[0])
	}
}

// --- Phase 4 SC1: network & firewall vendor pack ---------------------------

// TestPack_DellPowerConnectNotServer: the Dell PEN .674 is shared by PowerEdge
// servers and PowerConnect SWITCHES (.674.10895). The broad .674→server rule must
// be EXCLUDED for the networking subtree, and the .674.10895→switch rule wins.
func TestPack_DellPowerConnectNotServer(t *testing.T) {
	lib := Library()
	ev := Evidence{SysObjectID: "1.3.6.1.4.1.674.10895.3041", SysDescr: "Dell Networking N3048"}
	winners, rejected := MatchWithRejected(ev, lib)
	if len(winners) == 0 || winners[0].DeviceType != "switch" {
		t.Fatalf("Dell PowerConnect should classify as switch, got winners=%+v", winners)
	}
	for _, w := range winners {
		if w.DeviceType == "server" {
			t.Errorf("no server candidate should survive for a Dell switch: %+v", w)
		}
	}
	var sawExcluded bool
	for _, rj := range rejected {
		if rj.DeviceType == "server" && rj.Pattern == "1.3.6.1.4.1.674" && contains(rj.Reason, "excluded") {
			sawExcluded = true
		}
	}
	if !sawExcluded {
		t.Errorf("expected the .674 server rule excluded for the networking subtree, rejected=%+v", rejected)
	}
}

// TestPack_DellServerStillServer: a Dell PowerEdge (OpenManage .674.10892) is NOT
// in the excluded networking subtree, so .674→server still classifies it.
func TestPack_DellServerStillServer(t *testing.T) {
	ev := Evidence{SysObjectID: "1.3.6.1.4.1.674.10892.1", SysDescr: "Dell OpenManage"}
	res := Match(ev, Library())
	if len(res) == 0 || res[0].DeviceType != "server" {
		t.Fatalf("Dell PowerEdge should stay server, got %+v", res)
	}
}

// TestPack_LoadBalancers: F5/Citrix/A10 classify into the new load_balancer
// category via OID and sysDescr.
func TestPack_LoadBalancers(t *testing.T) {
	lib := Library()
	cases := []struct {
		name string
		ev   Evidence
	}{
		{"F5 OID", Evidence{SysObjectID: "1.3.6.1.4.1.3375.2.1.3.4.43"}},
		{"F5 descr", Evidence{SysDescr: "BIG-IP 15.1.0 Build 0.0.31"}},
		{"NetScaler", Evidence{SysDescr: "NetScaler NS13.0"}},
		{"A10", Evidence{SysObjectID: "1.3.6.1.4.1.22610.1.3.11"}},
		{"Kemp", Evidence{SysDescr: "LoadMaster by Kemp"}},
	}
	for _, c := range cases {
		res := Match(c.ev, lib)
		if len(res) == 0 || res[0].DeviceType != "load_balancer" {
			t.Errorf("%s: expected load_balancer, got %+v", c.name, res)
		}
	}
}

// TestPack_Firewalls: the new firewall pack entries classify into firewall.
func TestPack_Firewalls(t *testing.T) {
	lib := Library()
	cases := []struct {
		name string
		ev   Evidence
	}{
		{"PAN-OS", Evidence{SysDescr: "Palo Alto Networks PA-220 PAN-OS 10.1"}},
		{"SonicWall OID", Evidence{SysObjectID: "1.3.6.1.4.1.8741.1"}},
		{"WatchGuard OID", Evidence{SysObjectID: "1.3.6.1.4.1.3097.1"}},
		{"Check Point", Evidence{SysDescr: "Check Point Gaia R81"}},
		{"pfSense", Evidence{SysDescr: "pfSense firewall"}},
		{"OPNsense", Evidence{SysDescr: "OPNsense 23.7"}},
		{"Juniper SRX", Evidence{SysObjectID: "1.3.6.1.4.1.2636.1.1.1", SysDescr: "Juniper SRX340 JUNOS"}},
	}
	for _, c := range cases {
		res := Match(c.ev, lib)
		if len(res) == 0 || res[0].DeviceType != "firewall" {
			t.Errorf("%s: expected firewall, got %+v", c.name, res)
		}
	}
}

// TestPack_NewSwitchVendors: the new switch-vendor PENs classify into switch.
func TestPack_NewSwitchVendors(t *testing.T) {
	lib := Library()
	for _, oid := range []string{
		"1.3.6.1.4.1.171.10",   // D-Link
		"1.3.6.1.4.1.25506.11", // H3C
		"1.3.6.1.4.1.4881.1",   // Ruijie
		"1.3.6.1.4.1.207.1",    // Allied Telesis
	} {
		res := Match(Evidence{SysObjectID: oid}, lib)
		if len(res) == 0 || res[0].DeviceType != "switch" {
			t.Errorf("%s: expected switch, got %+v", oid, res)
		}
	}
}

// --- Phase 4 SC2: compute pack (server / BMC / virtualization / storage) ----

// topMatch is a small helper for the SC2 table tests.
func topMatch(ev Evidence) (Result, bool) {
	res := Match(ev, Library())
	if len(res) == 0 {
		return Result{}, false
	}
	return res[0], true
}

// TestPack_BMCsClassifyAsBMC: a DEFINITIVE out-of-band controller identity
// (iDRAC/iLO/XCC/Redfish) classifies as the "bmc" category — its own inventory view,
// not mixed with servers — carrying the vendor/model identity, and beats generic HTTP.
func TestPack_BMCsClassifyAsBMC(t *testing.T) {
	cases := []struct {
		name       string
		ev         Evidence
		wantVendor string
		wantModel  string // "" = don't care
	}{
		{"Dell iDRAC OID", Evidence{SysObjectID: "1.3.6.1.4.1.674.10892.2.1"}, "Dell", "iDRAC"},
		{"Dell iDRAC descr", Evidence{SysDescr: "Integrated Dell Remote Access Controller 9"}, "Dell", ""},
		{"HPE iLO", Evidence{SysDescr: "HP Integrated Lights-Out 5"}, "HPE", "iLO"},
		{"Lenovo XCC", Evidence{SysDescr: "Lenovo XClarity Controller"}, "Lenovo", "XClarity Controller"},
		{"Redfish HTTP", Evidence{HTTPServer: "Redfish/1.0"}, "Generic BMC", ""},
		{"iLO HTTP header", Evidence{HTTPServer: "HPE-iLO-Server/1.30"}, "HPE", "iLO"},
	}
	for _, c := range cases {
		top, ok := topMatch(c.ev)
		if !ok || top.DeviceType != "bmc" {
			t.Errorf("%s: expected bmc, got %+v", c.name, top)
			continue
		}
		if top.Vendor != c.wantVendor {
			t.Errorf("%s: vendor=%q want %q", c.name, top.Vendor, c.wantVendor)
		}
		if c.wantModel != "" && top.Model != c.wantModel {
			t.Errorf("%s: model=%q want %q", c.name, top.Model, c.wantModel)
		}
	}
}

// TestPack_PowerEdgeNotIDRAC: a Dell PowerEdge (OpenManage .674.10892.1, no BMC
// marker) classifies as server but must NOT pick up the iDRAC identity.
func TestPack_PowerEdgeNotIDRAC(t *testing.T) {
	ev := Evidence{SysObjectID: "1.3.6.1.4.1.674.10892.1.700", SysDescr: "Dell PowerEdge R740"}
	top, ok := topMatch(ev)
	if !ok || top.DeviceType != "server" {
		t.Fatalf("PowerEdge should be server, got %+v", top)
	}
	if top.Model == "iDRAC" {
		t.Errorf("PowerEdge must not become iDRAC without BMC evidence: %+v", top)
	}
}

// TestPack_HPEProLiantNotSwitch: an HPE ProLiant (Compaq PEN .232) must classify
// as server, never as an Aruba/HPE switch (the .11 ProCurve tree).
func TestPack_HPEProLiantNotSwitch(t *testing.T) {
	ev := Evidence{SysObjectID: "1.3.6.1.4.1.232.9.4.10", SysDescr: "HP ProLiant DL380 Gen10"}
	winners := Match(ev, Library())
	if len(winners) == 0 || winners[0].DeviceType != "server" {
		t.Fatalf("ProLiant should be server, got %+v", winners)
	}
	for _, w := range winners {
		if w.DeviceType == "switch" {
			t.Errorf("ProLiant must never produce a switch candidate: %+v", w)
		}
	}
}

// TestPack_Virtualization: ESXi/Proxmox/vCenter/Nutanix → virtual_host, beating
// generic Linux/SSH evidence.
func TestPack_Virtualization(t *testing.T) {
	cases := []Evidence{
		{SysDescr: "VMware ESXi 7.0.3 build-19482537", SSHBanner: "SSH-2.0-OpenSSH"},
		{SysDescr: "Proxmox VE 8.1", SSHBanner: "SSH-2.0-OpenSSH_9.2"},
		{SysDescr: "VMware vCenter Server Appliance"},
		{SysDescr: "Nutanix Controller VM"},
	}
	for _, ev := range cases {
		top, ok := topMatch(ev)
		if !ok || top.DeviceType != "virtual_host" {
			t.Errorf("%q: expected virtual_host, got %+v", ev.SysDescr, top)
		}
	}
}

// TestPack_StorageNAS: Synology/QNAP/TrueNAS/NetApp/EMC classify as storage
// (Synology + QNAP RECLASSIFIED from server — see commit note on behavior impact).
func TestPack_StorageNAS(t *testing.T) {
	cases := []Evidence{
		{SysObjectID: "1.3.6.1.4.1.6574.1"},      // Synology
		{SysObjectID: "1.3.6.1.4.1.24681.1.2.1"}, // QNAP
		{SysDescr: "TrueNAS-13.0-U6.1"},          // TrueNAS
		{SysObjectID: "1.3.6.1.4.1.789.2"},       // NetApp
		{SysDescr: "Dell EMC Isilon OneFS"},      // EMC Isilon
	}
	for _, ev := range cases {
		top, ok := topMatch(ev)
		if !ok || top.DeviceType != "storage" {
			t.Errorf("%+v: expected storage, got %+v", ev, top)
		}
	}
}

// TestPack_GenericLinuxStaysServer: a plain Linux host with only OS + SSH evidence
// must remain server — not promoted to ESXi/Proxmox/BMC/storage.
func TestPack_GenericLinuxStaysServer(t *testing.T) {
	ev := Evidence{SysDescr: "Linux db01 5.15.0-86-generic x86_64", SSHBanner: "SSH-2.0-OpenSSH_8.9p1"}
	top, ok := topMatch(ev)
	if !ok || top.DeviceType != "server" {
		t.Fatalf("generic Linux should stay server, got %+v", top)
	}
}

// TestPack_GenericHTTPNotBMC: a bare web server banner must not be classified as a
// BMC/management controller.
func TestPack_GenericHTTPNotBMC(t *testing.T) {
	top, ok := topMatch(Evidence{HTTPServer: "Apache/2.4.41 (Ubuntu)"})
	if !ok {
		t.Fatal("expected a match for Apache banner")
	}
	if top.Model == "iDRAC" || top.Model == "iLO" || top.Vendor == "Generic BMC" {
		t.Errorf("generic HTTP must not become a BMC: %+v", top)
	}
}

// --- Phase 4 SC3: edge pack (printer / UPS / PDU / CCTV / wireless-AP / VoIP) -

func TestPack_PrintersEdge(t *testing.T) {
	cases := []struct {
		name string
		ev   Evidence
	}{
		{"HP JetDirect", Evidence{SysObjectID: "1.3.6.1.4.1.11.2.3.9.1", SysDescr: "HP ETHERNET MULTI-ENVIRONMENT"}},
		{"Canon iR-ADV", Evidence{SysObjectID: "1.3.6.1.4.1.1602.1", SysDescr: "Canon iR-ADV C5560"}},
		{"Canon LBP", Evidence{SysDescr: "Canon LBP6030"}},
		{"Kyocera ECOSYS", Evidence{SysObjectID: "1.3.6.1.4.1.1347.43", SysDescr: "KYOCERA ECOSYS M2640idw"}},
		{"UTAX", Evidence{SysDescr: "UTAX 5006ci"}},
		{"Ricoh", Evidence{SysObjectID: "1.3.6.1.4.1.367.1"}},
		{"Xerox", Evidence{SysObjectID: "1.3.6.1.4.1.253.8"}},
		{"Brother", Evidence{SysObjectID: "1.3.6.1.4.1.2435.2"}},
		{"Epson", Evidence{SysObjectID: "1.3.6.1.4.1.1248.1"}},
		{"Lexmark", Evidence{SysObjectID: "1.3.6.1.4.1.641.1"}},
		{"Sharp", Evidence{SysObjectID: "1.3.6.1.4.1.2385.1"}},
		{"Konica bizhub", Evidence{SysObjectID: "1.3.6.1.4.1.18334.1", SysDescr: "KONICA MINOLTA bizhub C360"}},
		{"Toshiba", Evidence{SysObjectID: "1.3.6.1.4.1.1129.1"}},
	}
	for _, c := range cases {
		top, ok := topMatch(c.ev)
		if !ok || top.DeviceType != "printer" {
			t.Errorf("%s: expected printer, got %+v", c.name, top)
		}
	}
}

// TestPack_PrinterNotSwitch: an HP printer must never produce a switch candidate.
func TestPack_PrinterNotSwitch(t *testing.T) {
	winners := Match(Evidence{SysObjectID: "1.3.6.1.4.1.11.2.3.9.5", SysDescr: "HP LaserJet MFP M725"}, Library())
	if len(winners) == 0 || winners[0].DeviceType != "printer" {
		t.Fatalf("HP printer should be printer, got %+v", winners)
	}
	for _, w := range winners {
		if w.DeviceType == "switch" {
			t.Errorf("HP printer must not produce a switch candidate: %+v", w)
		}
	}
}

// TestPack_UPSvsPDU: APC UPS→ups, APC PDU→pdu (NOT ups — excluded), Eaton UPS→ups,
// Eaton/ServerTech/Raritan/Geist PDU→pdu, Tripp Lite UPS→ups.
func TestPack_UPSvsPDU(t *testing.T) {
	upsCases := []Evidence{
		{SysObjectID: "1.3.6.1.4.1.318.1.1.1.1"}, // APC Smart-UPS
		{SysObjectID: "1.3.6.1.4.1.534.1"},       // Eaton UPS
		{SysObjectID: "1.3.6.1.4.1.5491.1"},      // Tripp Lite UPS
		{SysObjectID: "1.3.6.1.4.1.3808.1"},      // CyberPower UPS
	}
	for _, ev := range upsCases {
		top, ok := topMatch(ev)
		if !ok || top.DeviceType != "ups" {
			t.Errorf("%+v: expected ups, got %+v", ev, top)
		}
	}
	pduCases := []Evidence{
		{SysObjectID: "1.3.6.1.4.1.318.1.1.4.5"},  // APC rPDU
		{SysObjectID: "1.3.6.1.4.1.318.1.1.12.1"}, // APC rPDU2
		{SysObjectID: "1.3.6.1.4.1.1718.3"},       // ServerTech
		{SysObjectID: "1.3.6.1.4.1.13742.6.1"},    // Raritan PX
		{SysObjectID: "1.3.6.1.4.1.21239.2"},      // Geist
		{SysDescr: "Eaton ePDU G3 Managed"},       // Eaton ePDU
	}
	for _, ev := range pduCases {
		winners := Match(ev, Library())
		if len(winners) == 0 || winners[0].DeviceType != "pdu" {
			t.Errorf("%+v: expected pdu, got %+v", ev, winners)
		}
		for _, w := range winners {
			if w.DeviceType == "ups" {
				t.Errorf("%+v: a PDU must not produce a ups candidate: %+v", ev, w)
			}
		}
	}
}

// TestPack_CCTV_NVRDVRvsCamera: NVR/DVR evidence beats the generic vendor camera.
func TestPack_CCTV_NVRDVRvsCamera(t *testing.T) {
	// Hikvision NVR: shares the .39165 camera PEN but the NVR web banner wins.
	nvr, _ := topMatch(Evidence{SysObjectID: "1.3.6.1.4.1.39165.1", HTTPServer: "DNVRS-Webs"})
	if nvr.DeviceType != "nvr" {
		t.Errorf("Hikvision NVR should be nvr, got %+v", nvr)
	}
	// DVR marker beats camera.
	dvr, _ := topMatch(Evidence{SysObjectID: "1.3.6.1.4.1.39165.1", SysDescr: "Hikvision Digital Video Recorder DS-7208"})
	if dvr.DeviceType != "dvr" {
		t.Errorf("Hikvision DVR should be dvr, got %+v", dvr)
	}
	// Plain camera stays camera.
	cam, _ := topMatch(Evidence{SysObjectID: "1.3.6.1.4.1.39165.1", HTTPServer: "App-webs"})
	if cam.DeviceType != "camera" {
		t.Errorf("Hikvision camera should be camera, got %+v", cam)
	}
}

// TestPack_WirelessAPs: AP product markers classify as access_point and beat both
// the generic vendor "wireless"→controller PEN and (for Omada) the TP-Link switch.
func TestPack_WirelessAPs(t *testing.T) {
	cases := []struct {
		name string
		ev   Evidence
	}{
		{"UniFi AP", Evidence{SysObjectID: "1.3.6.1.4.1.41112.1.4", SysDescr: "U6-Pro UniFi AP"}},
		{"Aruba Instant AP", Evidence{SysObjectID: "1.3.6.1.4.1.14823.1", SysDescr: "Aruba AP-515 (Instant AP)"}},
		{"Ruckus ZoneFlex", Evidence{SysDescr: "Ruckus Wireless ZoneFlex R610"}},
		{"Extreme Aerohive", Evidence{SysDescr: "Aerohive AP250"}},
	}
	for _, c := range cases {
		top, ok := topMatch(c.ev)
		if !ok || top.DeviceType != "access_point" {
			t.Errorf("%s: expected access_point, got %+v", c.name, top)
		}
	}
	// Omada EAP under the TP-Link .11863 switch PEN must become access_point, not switch.
	winners := Match(Evidence{SysObjectID: "1.3.6.1.4.1.11863.5", SysDescr: "EAP245(EU) 3.0"}, Library())
	if len(winners) == 0 || winners[0].DeviceType != "access_point" {
		t.Fatalf("Omada EAP should be access_point, got %+v", winners)
	}
	for _, w := range winners {
		if w.DeviceType == "switch" {
			t.Errorf("Omada AP must not classify as switch: %+v", w)
		}
	}
}

// TestPack_IPPhones: IP phones classify as ip_phone (not pbx, not endpoint).
func TestPack_IPPhones(t *testing.T) {
	cases := []struct {
		name string
		ev   Evidence
	}{
		{"Cisco IP Phone", Evidence{SysDescr: "Cisco IP Phone 8841"}},
		{"Yealink", Evidence{SysDescr: "Yealink SIP-T46G"}},
		{"Grandstream", Evidence{SysObjectID: "1.3.6.1.4.1.21342.3"}},
		{"Fanvil", Evidence{SysDescr: "Fanvil X3S"}},
		{"Polycom", Evidence{SysObjectID: "1.3.6.1.4.1.13885.1"}},
	}
	for _, c := range cases {
		top, ok := topMatch(c.ev)
		if !ok || top.DeviceType != "ip_phone" {
			t.Errorf("%s: expected ip_phone, got %+v", c.name, top)
		}
	}
}

// --- Phase 4 SC4: category-validity invariant ------------------------------

// TestLibraryCategoriesAreValid guards the whole built-in catalog: every print's
// device_type, after CanonicalCategory, MUST be a category the devices.category
// CHECK accepts (migrations 000066 + 000079) — otherwise scan-apply would fail to
// persist that classification. This catches a typo'd or unmapped device_type the
// moment it's added to the catalog.
func TestLibraryCategoriesAreValid(t *testing.T) {
	// Mirror of the devices.category CHECK set (keep in sync with migration 000092).
	valid := map[string]bool{}
	for _, c := range []string{
		"unknown", "network_device_unclassified", "switch", "router", "firewall", "access_point", "wireless_controller",
		"server", "virtual_host", "virtual_machine", "storage", "nvr", "dvr", "camera",
		"printer", "ip_phone", "pbx", "voice_gateway", "database", "directory", "dns",
		"dhcp", "fingerprint", "endpoint", "ups", "isp_router", "application",
		"load_balancer", "pdu",
		"bmc", "biometric", "biometric_device_unclassified", "pos", "pos_device_unclassified",
	} {
		valid[c] = true
	}
	for _, p := range Library() {
		cat := CanonicalCategory(p.DeviceType)
		if cat == "" || !valid[cat] {
			t.Errorf("fingerprint %s/%q emits device_type %q → category %q, which is NOT a valid devices.category",
				p.Kind, p.Pattern, p.DeviceType, cat)
		}
	}
}
