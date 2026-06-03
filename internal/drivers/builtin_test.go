package drivers

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
)

// The built-in registry must classify each vendor's switch to the right
// driver by its enterprise sysObjectID — the multi-driver disambiguation
// that Phase 2 introduces.
func TestBuiltin_ClassifiesByVendor(t *testing.T) {
	r := Builtin()
	cases := []struct {
		name string
		oid  string
		want string
	}{
		{"aruba", ".1.3.6.1.4.1.11.2.3.7.11.180", "aruba_hpe"},
		{"arubacx", ".1.3.6.1.4.1.47196.4.1.1", "aruba_hpe"},
		{"cisco", ".1.3.6.1.4.1.9.1.516", "cisco_ios"},
		{"huawei", ".1.3.6.1.4.1.2011.2.23.1", "huawei_vrp"},
		{"fortigate", ".1.3.6.1.4.1.12356.101.1.1", "fortigate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, m := r.Best(driver.Probe{SNMPSysObjectID: tc.oid})
			if d == nil {
				t.Fatalf("no driver matched %s", tc.oid)
			}
			if d.Name() != tc.want {
				t.Errorf("oid %s → %s, want %s", tc.oid, d.Name(), tc.want)
			}
			if m.Confidence != 90 {
				t.Errorf("expected authoritative (90) match, got %d", m.Confidence)
			}
		})
	}
}

func TestBuiltin_UnknownVendorNoMatch(t *testing.T) {
	r := Builtin()
	d, _ := r.Best(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.99999.1.1"})
	if d != nil {
		t.Fatalf("unknown enterprise OID should not match any driver, got %s", d.Name())
	}
}

func TestBuiltin_ServerByNetSnmpOID(t *testing.T) {
	r := Builtin()
	d, m := r.Best(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.8072.3.2.10", OpenTCPPorts: []int{161}})
	if d == nil || d.Name() != "host_snmp" {
		t.Fatalf("net-snmp OID should classify as host_snmp, got %v", d)
	}
	if m.Confidence != 80 {
		t.Errorf("expected conf=80, got %d", m.Confidence)
	}
}

// A Linux-based switch (vendor enterprise OID + "Linux" sysDescr) must
// classify as a SWITCH — the authoritative enterprise-OID match (90)
// outranks the server descr-heuristic (55). This is the exact class of bug
// behind the prior fleet's "linux switches" misclassification.
func TestBuiltin_LinuxBasedSwitchStaysSwitch(t *testing.T) {
	r := Builtin()
	d, _ := r.Best(driver.Probe{
		SNMPSysObjectID: ".1.3.6.1.4.1.11.2.3.7.11.180", // HP enterprise
		SNMPSysDescr:    "Linux-based ProCurve switch firmware",
		OpenTCPPorts:    []int{161},
	})
	if d == nil || d.Name() != "aruba_hpe" {
		t.Fatalf("Linux-based HP switch must classify as aruba_hpe, got %v", d)
	}
}

func TestBuiltin_AllRegistered(t *testing.T) {
	r := Builtin()
	got := map[string]bool{}
	for _, n := range r.Names() {
		got[n] = true
	}
	for _, want := range []string{"aruba_hpe", "cisco_ios", "huawei_vrp", "host_snmp", "fortigate", "vmware_esxi", "cctv", "wlan_controller", "redfish_bmc", "vmware_vsphere", "printer_snmp", "ups_snmp", "cucm"} {
		if !got[want] {
			t.Errorf("driver %s not registered", want)
		}
	}
}
