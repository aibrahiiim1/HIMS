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
		{Evidence{SysObjectID: "1.3.6.1.4.1.24681.1"}, "QNAP", "server"},
		{Evidence{SysObjectID: "1.3.6.1.4.1.21342.3"}, "Grandstream", "voip"},
		{Evidence{SysDescr: "Ruckus ZoneDirector 1200"}, "Ruckus Wireless", "wireless_controller"},
		{Evidence{SysDescr: "Alcatel-Lucent OmniSwitch 6450"}, "Alcatel-Lucent Enterprise", "switch"},
		{Evidence{SysDescr: "Yealink SIP-T46G"}, "Yealink", "voip"},
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
