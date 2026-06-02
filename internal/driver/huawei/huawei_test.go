package huawei

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestHuawei_FingerprintByOID(t *testing.T) {
	m := New().Fingerprint(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.2011.2.23.x"})
	if m.Confidence != 90 || m.Category != domain.CatSwitch {
		t.Fatalf("got %+v, want conf=90 switch", m)
	}
}

func TestHuawei_FingerprintByDescr(t *testing.T) {
	m := New().Fingerprint(driver.Probe{
		OpenTCPPorts: []int{161},
		SNMPSysDescr: "Huawei Versatile Routing Platform Software VRP (R) software, Version 5.170",
	})
	if m.Confidence != 70 {
		t.Fatalf("descr match got %+v, want conf=70", m)
	}
}

func TestHuawei_NoMatchForCisco(t *testing.T) {
	m := New().Fingerprint(driver.Probe{
		SNMPSysObjectID: ".1.3.6.1.4.1.9.1.516",
		OpenTCPPorts:    []int{161},
	})
	if m.Confidence != 0 {
		t.Fatalf("Cisco must not match huawei, got %+v", m)
	}
}
