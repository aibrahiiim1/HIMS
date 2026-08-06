package discovery

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParseTargets turns an operator-supplied target spec into a deduplicated,
// order-preserving list of host addresses, capped at maxHosts. It accepts a
// mix of tokens separated by commas, whitespace, semicolons, or newlines —
// each token being one of:
//
//	Single IP    10.20.0.5
//	CIDR         172.21.96.0/24
//	IP range     172.21.96.1-172.21.96.254   (full end address)
//	             172.21.96.1-254              (last-octet shorthand)
//
// It errors (rather than truncating) when the expanded total would exceed
// maxHosts, so the operator re-scopes deliberately. This is the single entry
// point the scan API uses for the Single-IP / IP-Range / Subnet modes.
func ParseTargets(spec string, maxHosts int) ([]netip.Addr, error) {
	hosts, _, err := ParseTargetsDetailed(spec, maxHosts)
	return hosts, err
}

// tokenizeTargets splits a target spec on the accepted delimiters. Shared so
// AssertedTargets and ParseTargetsDetailed can never disagree about what counts
// as one token.
func tokenizeTargets(spec string) []string {
	return strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

// AssertedTargets returns only the addresses the operator named individually —
// bare IP tokens — WITHOUT expanding any CIDR or range. Cheap enough to call on
// a huge spec, because it never enumerates.
//
// "Asserted" means the operator stated this device exists, which is what lets
// the liveness sweep admit it on weak evidence instead of reporting nothing.
func AssertedTargets(spec string) map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	for _, tok := range tokenizeTargets(spec) {
		if strings.ContainsAny(tok, "/-") {
			continue // CIDR or range: an expansion, not an assertion
		}
		if a, err := netip.ParseAddr(tok); err == nil {
			out[a] = true
		}
	}
	return out
}

// ParseTargetsDetailed is ParseTargets plus the set of addresses the operator
// named ONE BY ONE (a bare IP token), as opposed to addresses produced by
// expanding a CIDR or a range.
//
// The distinction matters for liveness. Sweeping a /24 and finding an address
// that answers only on a port a middlebox serves is not evidence of a device —
// suppressing it is what stops phantom enrolment. But when the operator TYPES a
// single address they are asserting the device exists, and answering "nothing
// found" ignores a direct instruction. Asserted addresses are therefore enrolled
// with an honest liveness caveat rather than dropped.
func ParseTargetsDetailed(spec string, maxHosts int) (hosts []netip.Addr, asserted map[netip.Addr]bool, err error) {
	asserted = map[netip.Addr]bool{}
	tokens := tokenizeTargets(spec)
	if len(tokens) == 0 {
		return nil, nil, fmt.Errorf("discovery: no targets provided")
	}

	seen := make(map[netip.Addr]struct{})
	out := make([]netip.Addr, 0, len(tokens))
	add := func(a netip.Addr) error {
		if _, dup := seen[a]; dup {
			return nil
		}
		if len(out) >= maxHosts {
			return fmt.Errorf("discovery: targets expand to more than %d hosts; re-scope", maxHosts)
		}
		seen[a] = struct{}{}
		out = append(out, a)
		return nil
	}

	for _, tok := range tokens {
		switch {
		case strings.Contains(tok, "/"):
			p, perr := netip.ParsePrefix(tok)
			if perr != nil {
				return nil, nil, fmt.Errorf("discovery: invalid CIDR %q: %w", tok, perr)
			}
			expanded, xerr := ExpandCIDR(p, maxHosts)
			if xerr != nil {
				return nil, nil, xerr
			}
			for _, h := range expanded {
				if aerr := add(h); aerr != nil {
					return nil, nil, aerr
				}
			}
		case strings.Contains(tok, "-"):
			expanded, xerr := expandRange(tok, maxHosts)
			if xerr != nil {
				return nil, nil, xerr
			}
			for _, h := range expanded {
				if aerr := add(h); aerr != nil {
					return nil, nil, aerr
				}
			}
		default:
			a, perr := netip.ParseAddr(tok)
			if perr != nil {
				return nil, nil, fmt.Errorf("discovery: invalid IP %q: %w", tok, perr)
			}
			if aerr := add(a); aerr != nil {
				return nil, nil, aerr
			}
			// The operator named this address explicitly.
			asserted[a] = true
		}
	}
	return out, asserted, nil
}

// FilterExcluded removes from hosts every address the exclude spec resolves to.
// The exclude spec accepts the SAME tokens as ParseTargets (single IP, IP range,
// CIDR, or a comma/space/newline-separated mix), so an operator scanning a range
// or subnet can carve out one or more IPs they don't want touched. An empty spec
// is a no-op. Returns the filtered list (input order preserved) + the count
// removed. Excluded entries that fall outside the scan scope simply match nothing.
func FilterExcluded(hosts []netip.Addr, excludeSpec string, maxHosts int) ([]netip.Addr, int, error) {
	if strings.TrimSpace(excludeSpec) == "" {
		return hosts, 0, nil
	}
	ex, err := ParseTargets(excludeSpec, maxHosts)
	if err != nil {
		return nil, 0, fmt.Errorf("exclude: %w", err)
	}
	drop := make(map[netip.Addr]struct{}, len(ex))
	for _, a := range ex {
		drop[a] = struct{}{}
	}
	out := make([]netip.Addr, 0, len(hosts))
	removed := 0
	for _, h := range hosts {
		if _, skip := drop[h]; skip {
			removed++
			continue
		}
		out = append(out, h)
	}
	return out, removed, nil
}

// expandRange enumerates an inclusive IPv4 range. Accepts both the full-end
// form (10.0.0.1-10.0.0.50) and the last-octet shorthand (10.0.0.1-50).
func expandRange(tok string, maxHosts int) ([]netip.Addr, error) {
	lo, hi, ok := strings.Cut(tok, "-")
	if !ok {
		return nil, fmt.Errorf("discovery: invalid range %q", tok)
	}
	start, err := netip.ParseAddr(strings.TrimSpace(lo))
	if err != nil {
		return nil, fmt.Errorf("discovery: invalid range start %q: %w", lo, err)
	}
	if !start.Is4() {
		return nil, fmt.Errorf("discovery: ranges are IPv4-only (got %q)", tok)
	}
	hi = strings.TrimSpace(hi)

	var end netip.Addr
	if strings.Contains(hi, ".") {
		end, err = netip.ParseAddr(hi)
		if err != nil {
			return nil, fmt.Errorf("discovery: invalid range end %q: %w", hi, err)
		}
		if !end.Is4() {
			return nil, fmt.Errorf("discovery: ranges are IPv4-only (got %q)", tok)
		}
	} else {
		// Last-octet shorthand: replace the final octet of start.
		oct := start.As4()
		var lastN int
		if _, err := fmt.Sscanf(hi, "%d", &lastN); err != nil || lastN < 0 || lastN > 255 {
			return nil, fmt.Errorf("discovery: invalid range end octet %q", hi)
		}
		oct[3] = byte(lastN)
		end = netip.AddrFrom4(oct)
	}

	s, e := start.As4(), end.As4()
	su := uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
	eu := uint32(e[0])<<24 | uint32(e[1])<<16 | uint32(e[2])<<8 | uint32(e[3])
	if eu < su {
		return nil, fmt.Errorf("discovery: range end %s precedes start %s", end, start)
	}
	if eu-su+1 > uint32(maxHosts) {
		return nil, fmt.Errorf("discovery: range %s spans %d hosts, exceeds max %d", tok, eu-su+1, maxHosts)
	}
	out := make([]netip.Addr, 0, eu-su+1)
	for v := su; v <= eu; v++ {
		out = append(out, netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}))
		if v == ^uint32(0) {
			break // guard against wrap at 255.255.255.255
		}
	}
	return out, nil
}
