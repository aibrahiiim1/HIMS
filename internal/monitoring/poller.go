package monitoring

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/coralsearesorts/hims/internal/snmp"
)

// DefaultPorts maps a device category to the TCP port the reachability check
// dials by default. These are management/control ports the device class is
// expected to listen on, chosen to be a meaningful "is it alive" signal
// rather than an arbitrary ping.
var DefaultPorts = map[string]int{
	"switch":              22, // SSH mgmt
	"router":              22,
	"firewall":            443, // mgmt UI / SSL-VPN
	"server":              22,
	"virtual_host":        443,
	"storage":             443,
	"nvr":                 80,
	"camera":              80,
	"printer":             9100, // raw print / JetDirect
	"wireless_controller": 443,
	"access_point":        22,
}

// DefaultPort returns the dial port for a category, falling back to 443 for
// anything unmapped (a TLS mgmt port is the most common open control plane).
func DefaultPort(category string) int {
	if p, ok := DefaultPorts[category]; ok {
		return p
	}
	return 443
}

// WindowsLivenessPorts are the management ports a Windows host (server OR
// workstation) is expected to answer on. Windows boxes typically run none of
// SSH/22 or 443, so the category default ports are the wrong liveness signal
// for them — a Win11 workstation collected over WinRM would otherwise be marked
// permanently "down". RDP/WinRM/SMB are the real always-on Windows surfaces.
var WindowsLivenessPorts = []int{3389, 5985, 445, 135}

// DefaultPortForDevice picks the reachability port for a (category, os_family).
// Windows hosts use a Windows management port (RDP) regardless of category, so
// servers and workstations alike get a port they actually serve; everything
// else uses the category default.
func DefaultPortForDevice(category, osFamily string) int {
	if osFamily == "windows" {
		return 3389 // RDP — the most universally enabled Windows mgmt port
	}
	return DefaultPort(category)
}

// reachabilityPref ranks ports by how meaningful they are as a management/
// liveness signal, so when a host answered on several we pick the best one.
var reachabilityPref = []int{443, 8443, 22, 3389, 5985, 5986, 80, 8080, 8000, 9100, 445, 135, 23, 161}

// ReachabilityPort chooses the TCP port the reachability check should dial. It
// PREFERS a port the host actually answered on during discovery (openPorts), so
// a host that is up is never marked "down" for a port it doesn't serve — the
// previous behaviour of dialing a single category-default port (e.g. 443 for a
// Windows workstation, or 22 for a switch with SSH disabled) produced false
// "offline" flapping. When no open ports are known (e.g. an imported device that
// was never scanned), it falls back to the OS-aware category default.
func ReachabilityPort(category, osFamily string, openPorts []int) int {
	if len(openPorts) > 0 {
		open := make(map[int]bool, len(openPorts))
		for _, p := range openPorts {
			open[p] = true
		}
		for _, p := range reachabilityPref {
			if open[p] {
				return p
			}
		}
		return openPorts[0] // any answered port proves the host is up
	}
	return DefaultPortForDevice(category, osFamily)
}

// DialFunc dials a network address with a context. Production uses
// net.Dialer.DialContext; tests substitute a fake so the poller is exercised
// without real sockets.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Result is the outcome of one probe.
type Result struct {
	OK      bool
	Latency time.Duration
	Err     error
	Value   *float64 // numeric value for metric probes (e.g. SNMP gauge)
}

// SysUpTimeOID is the default OID an SNMP metric check polls when none is set
// — a reachable, always-present liveness signal (sysUpTime.0, in ticks).
const SysUpTimeOID = "1.3.6.1.2.1.1.3.0"

// Poller executes reachability probes. It holds only a dialer + timeout, so
// it is cheap to construct and safe for concurrent use.
type Poller struct {
	dial    DialFunc
	timeout time.Duration
}

// NewPoller builds a Poller. A nil dial uses the standard net dialer; a
// zero timeout defaults to 3s.
func NewPoller(dial DialFunc, timeout time.Duration) *Poller {
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Poller{dial: dial, timeout: timeout}
}

// ProbeTCP attempts a TCP connection to addr:port. A successful handshake
// (connection established, then immediately closed) is the liveness signal;
// the round-trip time is recorded as latency. This needs no credentials and
// no raw-socket privilege, so it runs identically on dev and prod.
func (p *Poller) ProbeTCP(ctx context.Context, addr netip.Addr, port int) Result {
	if !addr.IsValid() {
		return Result{OK: false, Err: fmt.Errorf("invalid address")}
	}
	dialCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	target := net.JoinHostPort(addr.String(), fmt.Sprintf("%d", port))
	start := time.Now()
	conn, err := p.dial(dialCtx, "tcp", target)
	latency := time.Since(start)
	if err != nil {
		return Result{OK: false, Latency: latency, Err: err}
	}
	_ = conn.Close()
	return Result{OK: true, Latency: latency}
}

// ProbeTCPAny dials each port in turn and returns OK on the first that opens —
// a host is "up" if ANY of its expected management ports answers. Used for
// Windows hosts, which may expose RDP or WinRM or SMB depending on policy, so no
// single fixed port is a reliable liveness signal. Returns the failure of the
// last attempted port when none open.
func (p *Poller) ProbeTCPAny(ctx context.Context, addr netip.Addr, ports []int) Result {
	if !addr.IsValid() {
		return Result{OK: false, Err: fmt.Errorf("invalid address")}
	}
	var last Result
	for _, port := range ports {
		if ctx.Err() != nil {
			return Result{OK: false, Err: ctx.Err()}
		}
		last = p.ProbeTCP(ctx, addr, port)
		if last.OK {
			return last
		}
	}
	return last
}

// ProbeSNMP does an SNMP GET of one OID using the supplied community. Success
// (a value comes back) is the liveness signal; a numeric value is recorded.
// The community is used only in-memory and never logged. snmpClientFactory is
// overridable in tests so this runs without a real agent.
func (p *Poller) ProbeSNMP(ctx context.Context, addr netip.Addr, port int, community, oid string) Result {
	if !addr.IsValid() {
		return Result{OK: false, Err: fmt.Errorf("invalid address")}
	}
	return p.probeTarget(ctx, snmp.Target{
		Addr: addr, Port: uint16(normPort(port)), Version: snmp.V2c, Community: community, Timeout: p.timeout,
	}, oid)
}

// ProbeSNMPv3 does an SNMP v3 (USM) GET of one OID. The credentials are used
// only in-memory and never logged.
func (p *Poller) ProbeSNMPv3(ctx context.Context, addr netip.Addr, port int, v3 *snmp.V3Params, oid string) Result {
	if !addr.IsValid() {
		return Result{OK: false, Err: fmt.Errorf("invalid address")}
	}
	if v3 == nil {
		return Result{OK: false, Err: fmt.Errorf("nil v3 params")}
	}
	return p.probeTarget(ctx, snmp.Target{
		Addr: addr, Port: uint16(normPort(port)), Version: snmp.V3, V3: v3, Timeout: p.timeout,
	}, oid)
}

// probeTarget runs a single-OID GET against any SNMP target (v2c or v3).
func (p *Poller) probeTarget(ctx context.Context, tgt snmp.Target, oid string) Result {
	if oid == "" {
		oid = SysUpTimeOID
	}
	cl, err := snmpClientFactory(tgt)
	if err != nil {
		return Result{OK: false, Err: err}
	}
	defer cl.Close()

	start := time.Now()
	if err := cl.Connect(ctx); err != nil {
		return Result{OK: false, Latency: time.Since(start), Err: err}
	}
	pdus, err := cl.Get(ctx, oid)
	latency := time.Since(start)
	if err != nil {
		return Result{OK: false, Latency: latency, Err: err}
	}
	if len(pdus) == 0 {
		return Result{OK: false, Latency: latency, Err: fmt.Errorf("no value for %s", oid)}
	}
	return Result{OK: true, Latency: latency, Value: anyFloat(pdus[0].Value)}
}

func normPort(port int) int {
	if port <= 0 {
		return 161
	}
	return port
}

// snmpClientFactory builds an SNMP client; overridable in tests.
var snmpClientFactory = func(t snmp.Target) (snmp.Client, error) { return snmp.NewClient(t) }

// anyFloat coerces the common SNMP numeric types to a float64 pointer.
func anyFloat(v any) *float64 {
	var f float64
	switch n := v.(type) {
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint:
		f = float64(n)
	case uint32:
		f = float64(n)
	case uint64:
		f = float64(n)
	default:
		return nil
	}
	return &f
}
