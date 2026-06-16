package aruba

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestAruba_FingerprintBySysObjectID(t *testing.T) {
	d := New()
	for _, oid := range []string{
		".1.3.6.1.4.1.11.2.3.7.11.180", // HP ProCurve
		"1.3.6.1.4.1.14823.1.2.3",      // Aruba legacy
		".1.3.6.1.4.1.47196.4.1.1",     // ArubaOS-CX
	} {
		m := d.Fingerprint(driver.Probe{SNMPSysObjectID: oid})
		if m.Confidence != 90 || m.Category != domain.CatSwitch {
			t.Errorf("oid %s: got %+v, want conf=90 switch", oid, m)
		}
	}
}

func TestAruba_FingerprintByDescr(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{
		OpenTCPPorts: []int{161},
		SNMPSysDescr: "Aruba JL255A 2930F-24G-4SFP+ Switch, revision WC.16.10",
	})
	if m.Confidence != 70 || m.Category != domain.CatSwitch {
		t.Fatalf("descr match: got %+v, want conf=70 switch", m)
	}
}

func TestAruba_NoMatchForCisco(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{
		SNMPSysObjectID: ".1.3.6.1.4.1.9.1.516", // Cisco enterprise (9)
		SNMPSysDescr:    "Cisco IOS Software, C2960",
		OpenTCPPorts:    []int{161},
	})
	if m.Confidence != 0 {
		t.Fatalf("Cisco must not match aruba, got %+v", m)
	}
}

func TestAruba_DescrNeedsSNMPPort(t *testing.T) {
	d := New()
	// sysDescr keyword but no SNMP surface → not enough to claim.
	m := d.Fingerprint(driver.Probe{SNMPSysDescr: "HP something"})
	if m.Confidence != 0 {
		t.Fatalf("descr without SNMP port should not match, got %+v", m)
	}
}

// HP printers / JetDirect print servers share the HP enterprise OID (.11) with HP
// ProCurve switches but live under .11.2.3.9 and report "HP ETHERNET MULTI-
// ENVIRONMENT" — they must NOT be claimed as switches (regression: 172.21.60.42 /
// .39 / .73 / .159 were wrongly given the aruba_hpe switch driver/template).
func TestAruba_NoMatchForHPPrinter(t *testing.T) {
	d := New()
	cases := []driver.Probe{
		{SNMPSysObjectID: ".1.3.6.1.4.1.11.2.3.9.1", SNMPSysDescr: "HP ETHERNET MULTI-ENVIRONMENT", OpenTCPPorts: []int{161, 9100}},
		{SNMPSysDescr: "HP ETHERNET MULTI-ENVIRONMENT,SN:CNB7G3PCQ7,PID:HP LaserJet", OpenTCPPorts: []int{161, 9100}},
		{SNMPSysDescr: "HP ETHERNET MULTI-ENVIRONMENT,ROM none,JETDIRECT,JD149", OpenTCPPorts: []int{161, 9100}},
	}
	for _, p := range cases {
		if m := d.Fingerprint(p); m.Confidence != 0 {
			t.Errorf("HP printer must NOT match aruba switch: %q → %+v", p.SNMPSysDescr, m)
		}
	}
	// A real HP ProCurve switch (…11.2.3.7…) must STILL match — no regression.
	if m := d.Fingerprint(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.11.2.3.7.11.180", SNMPSysDescr: "ProCurve J9..."}); m.Confidence != 90 {
		t.Fatalf("real ProCurve switch must still match: %+v", m)
	}
}

func TestAruba_NameAndTemplate(t *testing.T) {
	d := New()
	if d.Name() != "aruba_hpe" || d.Template() != "switch" {
		t.Fatalf("unexpected name/template: %s / %s", d.Name(), d.Template())
	}
}
