package esxi

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestFingerprint_VMwareOID(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.6876.4.1", OpenTCPPorts: []int{161}})
	if m.Confidence != 90 || m.Category != domain.CatVirtualHost {
		t.Fatalf("VMware OID = %+v; want conf=90 virtual_host", m)
	}
}

func TestFingerprint_DescrHeuristic(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{SNMPSysDescr: "VMware ESXi 7.0.3 build-19193900", OpenTCPPorts: []int{161}})
	if m.Confidence != 70 || m.Category != domain.CatVirtualHost {
		t.Fatalf("ESXi descr = %+v; want conf=70 virtual_host", m)
	}
}

func TestFingerprint_NoMatch(t *testing.T) {
	d := New()
	if m := d.Fingerprint(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.9.1.1"}); m.Confidence != 0 {
		t.Fatalf("cisco OID should not match esxi; got %+v", m)
	}
}

func TestTemplateAndName(t *testing.T) {
	d := New()
	if d.Name() != "vmware_esxi" || d.Template() != "virtual_host" {
		t.Fatalf("name/template = %s/%s", d.Name(), d.Template())
	}
}
