package discovery

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
)

func TestFingerprintFromPorts(t *testing.T) {
	// SNMP is always on (UDP, not TCP-detectable).
	empty := fingerprintFromPorts(nil)
	if !empty.SNMP || empty.SSH || empty.WinRM || empty.HTTP || empty.LDAP {
		t.Errorf("no ports → %+v, want only SNMP", empty)
	}
	fp := fingerprintFromPorts([]int{22, 443, 5985, 389})
	if !fp.SNMP || !fp.SSH || !fp.WinRM || !fp.HTTP || !fp.LDAP {
		t.Errorf("22/443/5985/389 → %+v, want all true", fp)
	}
	if got := fingerprintFromPorts([]int{5986}); !got.WinRM {
		t.Error("5986 should enable WinRM")
	}
	if got := fingerprintFromPorts([]int{80}); !got.HTTP || got.SSH {
		t.Errorf("80 → %+v, want HTTP only (besides SNMP)", got)
	}
}

func TestPortAllowsProto(t *testing.T) {
	if !portAllowsProto([]int{5985}, domain.CredWinRM) {
		t.Error("WinRM should be allowed on 5985")
	}
	if portAllowsProto([]int{3389}, domain.CredWinRM) {
		t.Error("WinRM must NOT be probed when only RDP is open")
	}
	if !portAllowsProto([]int{22}, domain.CredSSH) {
		t.Error("SSH allowed on 22")
	}
	if !portAllowsProto([]int{443}, domain.CredONVIF) {
		t.Error("ONVIF allowed on a web port")
	}
	if portAllowsProto([]int{22}, domain.CredSNMPv2c) {
		t.Error("SNMP is handled separately, not via portAllowsProto")
	}
}

func TestProvisionalCategory(t *testing.T) {
	cases := map[domain.CredentialKind]domain.DeviceCategory{
		domain.CredWinRM:     domain.CatEndpoint,
		domain.CredSSH:       domain.CatServer,
		domain.CredONVIF:     domain.CatCamera,
		domain.CredSNMPv2c:   domain.CatUnknown,
		domain.CredHTTPBasic: domain.CatUnknown,
	}
	for k, want := range cases {
		if got := provisionalCategory(k); got != want {
			t.Errorf("provisionalCategory(%s) = %s, want %s", k, got, want)
		}
	}
}
