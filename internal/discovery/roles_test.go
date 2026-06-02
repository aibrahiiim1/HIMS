package discovery

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
)

func hasRole(roles []domain.DeviceRole, want domain.DeviceRole) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

func TestInferRoles_DomainController(t *testing.T) {
	// Kerberos + LDAP + DNS + DHCP → DC + DNS + DHCP.
	roles := InferRoles([]int{53, 88, 389, 67})
	for _, want := range []domain.DeviceRole{domain.RoleDomainController, domain.RoleDNS, domain.RoleDHCP} {
		if !hasRole(roles, want) {
			t.Errorf("expected role %s, got %v", want, roles)
		}
	}
}

func TestInferRoles_LDAPWithoutKerberosIsNotDC(t *testing.T) {
	// LDAP alone (no Kerberos) should NOT assert Domain Controller.
	roles := InferRoles([]int{389})
	if hasRole(roles, domain.RoleDomainController) {
		t.Errorf("LDAP alone must not infer DC, got %v", roles)
	}
}

func TestInferRoles_Databases(t *testing.T) {
	roles := InferRoles([]int{1433, 1521, 5432})
	for _, want := range []domain.DeviceRole{domain.RoleSQLServer, domain.RoleOracle, domain.RolePostgreSQL} {
		if !hasRole(roles, want) {
			t.Errorf("expected role %s, got %v", want, roles)
		}
	}
}

func TestInferRoles_NoServicePorts(t *testing.T) {
	roles := InferRoles([]int{22, 443})
	if len(roles) != 0 {
		t.Errorf("ssh/https alone infer no service roles, got %v", roles)
	}
}
