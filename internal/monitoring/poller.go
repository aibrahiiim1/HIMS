package monitoring

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"
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
}

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
