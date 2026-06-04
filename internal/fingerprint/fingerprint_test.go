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
