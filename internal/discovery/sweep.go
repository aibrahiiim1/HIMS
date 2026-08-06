package discovery

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Liveness sweep — the pass that answers "how many of these addresses are real"
// BEFORE any deep probing, credential attempt or enrolment happens.
//
// Why this exists: aliveness used to be "any TCP port answered". That is wrong
// on a network with a middlebox. A SIP ALG on the path answers TCP/5060 for
// EVERY address in the voice subnets, so a /24 holding ~61 real devices scanned
// as 254 alive and enrolled 193 devices that do not exist — 193 of them with
// [5060] as their only evidence.
//
// The fix is evidence-driven and device-agnostic: find the ports that answer on
// addresses which CANNOT host anything, treat those ports as untrustworthy for
// liveness in this scan, and say so out loud. No hardcoded port list, so the
// next ALG (HTTP, TLS, DNS) is caught the same way.

// PromiscuousPort is a port that answered where nothing real can live.
type PromiscuousPort struct {
	Port int
	// Reason is operator-facing and states the evidence, not a verdict.
	Reason string
	// OpenCount / Total give the observed rate across scanned addresses.
	OpenCount int
	Total     int
	// ViaControl is true when a control address proved it (definitive), false
	// when only the rate heuristic flagged it (strong, but inferential).
	ViaControl bool
}

// SweepConfig parameterises the liveness pass.
type SweepConfig struct {
	// Ports, when set, is probed verbatim for every address (tests use this).
	// When empty the per-host set from PortsForHost(ip, ExtraPorts) is used, so
	// the sweep probes exactly what the deep pipeline would.
	Ports       []int
	ExtraPorts  []int
	Timeout     time.Duration
	Concurrency int

	// Controls are addresses that cannot host a real device — for an IPv4
	// prefix of /30 or wider these are the network and broadcast addresses,
	// which ExpandCIDR deliberately excludes from the scan. A port answering
	// here is PROOF that something on the path replies on behalf of addresses
	// that do not exist. Empty for target-list scans; the rate heuristic then
	// carries the detection alone.
	Controls []netip.Addr

	// Asserted are addresses the operator named explicitly (a typed IP, not a
	// CIDR/range expansion). Such an address is treated as alive when it answers
	// at all — even if only on a port a middlebox serves — because the operator
	// asserted the device exists and returning "nothing found" ignores a direct
	// instruction. The weak evidence is not hidden: the address is reported in
	// LivenessUnproven so enrolment can flag it honestly. Sweeps of a CIDR are
	// unaffected, which is what keeps phantom enrolment suppressed.
	Asserted map[netip.Addr]bool

	// OnAddress, when set, is called as each address finishes probing, so the
	// caller can stream results while the sweep runs instead of waiting for the
	// whole pass. trusted reflects the CONTROL-proved promiscuous set, which is
	// known before the host pass starts; the rate heuristic can only demote a
	// port later, so a late-flagged port is corrected in the final result.
	OnAddress func(ip netip.Addr, open []int, trusted bool)

	// PromiscuousRate is the open-rate at or above which a port is treated as
	// promiscuous when no control proved it. Real subnets are never near-fully
	// populated on a single port, but a small fully-populated range legitimately
	// can be — hence MinSampleForRate.
	PromiscuousRate  float64
	MinSampleForRate int
}

// portsFor is the port set probed for one address.
func (c *SweepConfig) portsFor(ip netip.Addr) []int {
	if len(c.Ports) > 0 {
		return c.Ports
	}
	return PortsForHost(ip, c.ExtraPorts)
}

func (c *SweepConfig) withDefaults() {
	if c.Timeout <= 0 {
		c.Timeout = 500 * time.Millisecond
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 64
	}
	if c.PromiscuousRate <= 0 {
		c.PromiscuousRate = 0.95
	}
	if c.MinSampleForRate <= 0 {
		c.MinSampleForRate = 32
	}
}

// SweepResult is the outcome of the liveness pass.
type SweepResult struct {
	Total       int
	OpenByHost  map[netip.Addr][]int
	PortOpen    map[int]int
	Promiscuous []PromiscuousPort

	// Alive are addresses with at least one open port that is NOT promiscuous.
	Alive []netip.Addr
	// SuppressedByMiddlebox are addresses whose ONLY evidence was a promiscuous
	// port. They are NOT alive, but they are reported rather than silently
	// dropped: on a network with a SIP ALG a genuine phone that exposes only
	// 5060 is indistinguishable from an empty address by TCP alone, and hiding
	// that ambiguity would be dishonest.
	SuppressedByMiddlebox []netip.Addr
	// NoResponse answered nothing at all.
	NoResponse []netip.Addr
	// LivenessUnproven are Asserted addresses admitted to Alive on weak evidence
	// (only a middlebox-served port answered). They ARE enrolled — the operator
	// named them — but the caller must record why their liveness is unproven
	// rather than presenting them as ordinary discoveries.
	LivenessUnproven []netip.Addr
}

// AddressState is one scanned address and what the sweep concluded about it —
// the IP-scanner view of a scan: every address, not just the enrolled ones.
type AddressState struct {
	IP        string `json:"ip"`
	OpenPorts []int  `json:"open_ports,omitempty"`
	// State is "responded" (real evidence), "untrusted_only" (answered solely on
	// a port a middlebox is serving) or "silent" (no answer at all).
	State string `json:"state"`
}

// Addresses returns every scanned address with its verdict, lowest IP first.
// limit caps the slice (0 = no cap) so a huge scope cannot bloat the job record;
// the caller reports truncation rather than silently trimming.
func (r *SweepResult) Addresses(limit int) []AddressState {
	state := make(map[netip.Addr]string, r.Total)
	for _, a := range r.Alive {
		state[a] = "responded"
	}
	for _, a := range r.SuppressedByMiddlebox {
		state[a] = "untrusted_only"
	}
	for _, a := range r.NoResponse {
		state[a] = "silent"
	}
	ordered := make([]netip.Addr, 0, len(state))
	for a := range state {
		ordered = append(ordered, a)
	}
	sortAddrs(ordered)
	out := make([]AddressState, 0, len(ordered))
	for _, a := range ordered {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, AddressState{IP: a.String(), OpenPorts: r.OpenByHost[a], State: state[a]})
	}
	return out
}

// IsPromiscuous reports whether the sweep distrusts this port.
func (r *SweepResult) IsPromiscuous(port int) bool {
	for _, p := range r.Promiscuous {
		if p.Port == port {
			return true
		}
	}
	return false
}

// Summary is a one-line operator-facing description of what the sweep found.
func (r *SweepResult) Summary() string {
	s := fmt.Sprintf("%d of %d addresses responded", len(r.Alive), r.Total)
	if len(r.Promiscuous) > 0 {
		s += fmt.Sprintf("; %d port(s) answered for addresses that cannot exist and were excluded from liveness", len(r.Promiscuous))
	}
	if n := len(r.SuppressedByMiddlebox); n > 0 {
		s += fmt.Sprintf("; %d address(es) answered ONLY on such a port and were not enrolled", n)
	}
	if n := len(r.LivenessUnproven); n > 0 {
		s += fmt.Sprintf("; %d explicitly-targeted address(es) answered only on such a port and were enrolled anyway with liveness unproven", n)
	}
	return s
}

// ControlsFor returns the addresses that cannot host a device for a prefix —
// the IPv4 network and broadcast addresses of a /30-or-wider prefix. These are
// exactly what ExpandCIDR omits, so probing them costs nothing real and gives
// the sweep a negative control.
func ControlsFor(p netip.Prefix) []netip.Addr {
	if !p.IsValid() {
		return nil
	}
	p = p.Masked()
	if !p.Addr().Is4() || p.Addr().BitLen()-p.Bits() < 2 {
		return nil // /31, /32 and IPv6 have no unusable ends
	}
	network := p.Addr()
	// Broadcast = network | ^mask.
	b := network.As4()
	hostBits := 32 - p.Bits()
	var mask uint32
	for i := 0; i < hostBits; i++ {
		mask |= 1 << uint(i)
	}
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= mask
	broadcast := netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	return []netip.Addr{network, broadcast}
}

// StandardScanPorts is the broad management/service port set used by both the
// liveness sweep and the deep pipeline, so the sweep cannot decide a host is
// dead using narrower evidence than the pipeline would have used.
var StandardScanPorts = []int{
	22, 23, 53, 80, 88, 111, 135, 161, 389, 443, 445, 554, 636, 902, 1433, 1521,
	2049, 3389, 5060, 5061, 5432, 5985, 5986, 8000, 8008, 8010, 8080, 8443, 9100,
}

// PortsForHost is the exact TCP port set probed for one host: the standard
// management set, the operator's extra/web ports, and the Hikvision convention
// where a recorder's web/ISAPI port is 8000 + the host's last octet.
//
// Both the liveness sweep and the deep pipeline call this, so the sweep can
// never probe a narrower set than the pipeline would have and then hand back
// results the pipeline treats as complete.
func PortsForHost(ip netip.Addr, extra []int) []int {
	ports := append([]int(nil), StandardScanPorts...)
	if u := ip.Unmap(); u.Is4() {
		if octet := int(u.As4()[3]); octet >= 1 && octet <= 255 {
			ports = append(ports, 8000+octet)
		}
	}
	for _, p := range extra {
		if p > 0 && p < 65536 {
			ports = append(ports, p)
		}
	}
	return dedupInts(ports)
}

// ControlsForHosts derives negative-control addresses from a host list by
// taking the network + broadcast address of every distinct IPv4 /24 present.
// Those are precisely the addresses ExpandCIDR omits, so they cost nothing real
// and cannot be a device. Capped at maxSubnets so a huge multi-subnet scan does
// not spend its budget on controls.
func ControlsForHosts(hosts []netip.Addr, maxSubnets int) []netip.Addr {
	if maxSubnets <= 0 {
		maxSubnets = 16
	}
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, h := range hosts {
		u := h.Unmap()
		if !u.Is4() {
			continue
		}
		b := u.As4()
		network := netip.AddrFrom4([4]byte{b[0], b[1], b[2], 0})
		if seen[network] {
			continue
		}
		seen[network] = true
		if len(seen) > maxSubnets {
			break
		}
		out = append(out, network, netip.AddrFrom4([4]byte{b[0], b[1], b[2], 255}))
	}
	return out
}

// LivenessSweep TCP-probes every host (and every control) and classifies each
// address. progress, when non-nil, is called as addresses complete so the UI can
// show "scanned / alive" while the pass runs.
func LivenessSweep(ctx context.Context, hosts []netip.Addr, cfg SweepConfig, progress func(scanned, alive int)) SweepResult {
	cfg.withDefaults()
	res := SweepResult{
		Total:      len(hosts),
		OpenByHost: make(map[netip.Addr][]int, len(hosts)),
		PortOpen:   map[int]int{},
	}
	if len(hosts) == 0 {
		return res
	}

	// --- Pass 1: probe controls first. Knowing the promiscuous set up front
	// lets the progress counter report a truthful alive count as it goes.
	controlOpen := map[int]bool{}
	for _, c := range cfg.Controls {
		for _, p := range scanPorts(ctx, c, cfg.portsFor(c), cfg.Timeout) {
			controlOpen[p] = true
		}
	}

	// --- Pass 2: probe every host.
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	scanned := 0
	for _, h := range hosts {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ip netip.Addr) {
			defer wg.Done()
			defer func() { <-sem }()
			open := scanPorts(ctx, ip, cfg.portsFor(ip), cfg.Timeout)
			trusted := false
			for _, p := range open {
				if !controlOpen[p] {
					trusted = true
					break
				}
			}
			mu.Lock()
			res.OpenByHost[ip] = open
			for _, p := range open {
				res.PortOpen[p]++
			}
			scanned++
			n := scanned
			mu.Unlock()
			if cfg.OnAddress != nil {
				cfg.OnAddress(ip, open, trusted)
			}
			if progress != nil {
				progress(n, 0) // alive is only final after classification
			}
		}(h)
	}
	wg.Wait()

	// --- Decide which ports are untrustworthy.
	seen := map[int]bool{}
	for port := range controlOpen {
		res.Promiscuous = append(res.Promiscuous, PromiscuousPort{
			Port: port, OpenCount: res.PortOpen[port], Total: res.Total, ViaControl: true,
			Reason: fmt.Sprintf("answered on an address that cannot host a device (network/broadcast) — a device on the path is replying for the whole range, so port %d proves nothing about any address here", port),
		})
		seen[port] = true
	}
	if res.Total >= cfg.MinSampleForRate {
		for port, n := range res.PortOpen {
			if seen[port] {
				continue
			}
			if float64(n)/float64(res.Total) >= cfg.PromiscuousRate {
				res.Promiscuous = append(res.Promiscuous, PromiscuousPort{
					Port: port, OpenCount: n, Total: res.Total, ViaControl: false,
					Reason: fmt.Sprintf("answered on %d of %d addresses (%.0f%%) — implausible for a real subnet, so it is treated as answered by something on the path", n, res.Total, 100*float64(n)/float64(res.Total)),
				})
				seen[port] = true
			}
		}
	}
	sort.Slice(res.Promiscuous, func(i, j int) bool { return res.Promiscuous[i].Port < res.Promiscuous[j].Port })

	// --- Classify each address against the trusted evidence.
	for _, h := range hosts {
		open := res.OpenByHost[h]
		if len(open) == 0 {
			res.NoResponse = append(res.NoResponse, h)
			continue
		}
		trusted := false
		for _, p := range open {
			if !seen[p] {
				trusted = true
				break
			}
		}
		switch {
		case trusted:
			res.Alive = append(res.Alive, h)
		case cfg.Asserted[h]:
			// Operator named this address: admit it, but say the liveness is unproven.
			res.Alive = append(res.Alive, h)
			res.LivenessUnproven = append(res.LivenessUnproven, h)
		default:
			res.SuppressedByMiddlebox = append(res.SuppressedByMiddlebox, h)
		}
	}
	sortAddrs(res.Alive)
	sortAddrs(res.SuppressedByMiddlebox)
	sortAddrs(res.NoResponse)
	sortAddrs(res.LivenessUnproven)
	return res
}

func sortAddrs(a []netip.Addr) {
	sort.Slice(a, func(i, j int) bool { return a[i].Less(a[j]) })
}
