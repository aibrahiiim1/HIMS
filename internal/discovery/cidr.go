package discovery

import (
	"fmt"
	"net/netip"
)

// ExpandCIDR enumerates the host addresses in a prefix, capped at maxHosts to
// guard against an accidentally huge scope (e.g. a /8). For IPv4 prefixes of
// /30 or wider it skips the network and broadcast addresses; /31 and /32 (and
// all IPv6) yield every address in range. It errors rather than truncating
// when the host count would exceed maxHosts, so the operator re-scopes
// deliberately instead of silently scanning a fraction.
func ExpandCIDR(p netip.Prefix, maxHosts int) ([]netip.Addr, error) {
	if !p.IsValid() {
		return nil, fmt.Errorf("discovery: invalid prefix")
	}
	p = p.Masked()
	bits := p.Bits()
	total := p.Addr().BitLen() // 32 or 128

	// Host count = 2^(total-bits). Compute guardedly to avoid overflow.
	hostBits := total - bits
	if hostBits > 20 { // >~1M addresses — refuse before allocating
		return nil, fmt.Errorf("discovery: scope /%d too large (max %d hosts)", bits, maxHosts)
	}
	count := 1 << uint(hostBits)

	skipEnds := p.Addr().Is4() && hostBits >= 2 // skip network + broadcast on IPv4 /30..
	usable := count
	if skipEnds {
		usable -= 2
	}
	if usable > maxHosts {
		return nil, fmt.Errorf("discovery: scope has %d hosts, exceeds max %d", usable, maxHosts)
	}

	out := make([]netip.Addr, 0, usable)
	addr := p.Addr()
	for i := 0; i < count; i++ {
		isNetwork := i == 0
		isBroadcast := i == count-1
		if !(skipEnds && (isNetwork || isBroadcast)) {
			out = append(out, addr)
		}
		addr = addr.Next()
		if !addr.IsValid() {
			break
		}
	}
	return out, nil
}
