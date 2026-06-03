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
	tokens := strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(tokens) == 0 {
		return nil, fmt.Errorf("discovery: no targets provided")
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
			p, err := netip.ParsePrefix(tok)
			if err != nil {
				return nil, fmt.Errorf("discovery: invalid CIDR %q: %w", tok, err)
			}
			hosts, err := ExpandCIDR(p, maxHosts)
			if err != nil {
				return nil, err
			}
			for _, h := range hosts {
				if err := add(h); err != nil {
					return nil, err
				}
			}
		case strings.Contains(tok, "-"):
			hosts, err := expandRange(tok, maxHosts)
			if err != nil {
				return nil, err
			}
			for _, h := range hosts {
				if err := add(h); err != nil {
					return nil, err
				}
			}
		default:
			a, err := netip.ParseAddr(tok)
			if err != nil {
				return nil, fmt.Errorf("discovery: invalid IP %q: %w", tok, err)
			}
			if err := add(a); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
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
