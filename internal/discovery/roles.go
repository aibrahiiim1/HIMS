package discovery

import "github.com/coralsearesorts/hims/internal/domain"

// InferRoles derives candidate device roles from open ports — a cheap,
// SNMP-free signal that a server is also a DNS/DC/DB/etc. host. These are
// CANDIDATE roles (a listening port strongly suggests, but doesn't prove, a
// role); deeper confirmation (LDAP bind, SQL handshake) is a later phase.
// A device can hold several roles at once (ADR-0001 multi-role).
func InferRoles(openPorts []int) []domain.DeviceRole {
	set := map[int]struct{}{}
	for _, p := range openPorts {
		set[p] = struct{}{}
	}
	has := func(p int) bool { _, ok := set[p]; return ok }

	var roles []domain.DeviceRole
	add := func(r domain.DeviceRole) { roles = append(roles, r) }

	if has(53) {
		add(domain.RoleDNS)
	}
	if has(67) || has(68) {
		add(domain.RoleDHCP)
	}
	// 88 (Kerberos) + 389 (LDAP) together strongly indicate a Domain Controller.
	if has(88) && has(389) {
		add(domain.RoleDomainController)
	}
	if has(1433) {
		add(domain.RoleSQLServer)
	}
	if has(1521) {
		add(domain.RoleOracle)
	}
	if has(5432) {
		add(domain.RolePostgreSQL)
	}
	// File services: SMB (445) or NFS (2049). A bare management 443 is NOT a
	// web-server signal (too many appliances expose 443) — plain HTTP (80) is.
	if has(445) || has(2049) {
		add(domain.RoleFileServer)
	}
	if has(80) {
		add(domain.RoleWebServer)
	}
	return roles
}
