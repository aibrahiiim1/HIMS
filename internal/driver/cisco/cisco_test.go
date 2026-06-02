package cisco

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestCisco_FingerprintByOID(t *testing.T) {
	m := New().Fingerprint(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.9.1.516"})
	if m.Confidence != 90 || m.Category != domain.CatSwitch {
		t.Fatalf("got %+v, want conf=90 switch", m)
	}
}

func TestCisco_FingerprintByDescr(t *testing.T) {
	m := New().Fingerprint(driver.Probe{
		OpenTCPPorts: []int{161},
		SNMPSysDescr: "Cisco IOS Software, C2960 Software (C2960-LANBASEK9-M), Version 15.0(2)SE",
	})
	if m.Confidence != 70 {
		t.Fatalf("descr match got %+v, want conf=70", m)
	}
}

func TestCisco_NoMatchForHP(t *testing.T) {
	m := New().Fingerprint(driver.Probe{
		SNMPSysObjectID: ".1.3.6.1.4.1.11.2.3.7.11.180",
		SNMPSysDescr:    "HP ProCurve",
		OpenTCPPorts:    []int{161},
	})
	if m.Confidence != 0 {
		t.Fatalf("HP must not match cisco, got %+v", m)
	}
}

func TestCisco_NameTemplate(t *testing.T) {
	d := New()
	if d.Name() != "cisco_ios" || d.Template() != "switch" {
		t.Fatalf("name/template: %s / %s", d.Name(), d.Template())
	}
}
