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

// ProbeSNMP does an SNMP GET of one OID using the supplied community. Success
// (a value comes back) is the liveness signal; a numeric value is recorded.
// The community is used only in-memory and never logged. snmpClientFactory is
// overridable in tests so this runs without a real agent.
func (p *Poller) ProbeSNMP(ctx context.Context, addr netip.Addr, port int, community, oid string) Result {
	if !addr.IsValid() {
		return Result{OK: false, Err: fmt.Errorf("invalid address")}
	}
	if oid == "" {
		oid = SysUpTimeOID
	}
	if port <= 0 {
		port = 161
	}
	cl, err := snmpClientFactory(snmp.Target{
		Addr:      addr,
		Port:      uint16(port),
		Version:   snmp.V2c,
		Community: community,
		Timeout:   p.timeout,
	})
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
