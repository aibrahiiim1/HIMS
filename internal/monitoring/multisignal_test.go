package monitoring

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// dialOnlyPorts returns a DialFunc that "connects" only to the listed ports and
// refuses every other — modelling a device that answers tcp/5060 while its old
// monitored ports (3389/445) are dead (the 150.0.0.190 false-unreachable case).
func dialOnlyPorts(open ...int) DialFunc {
	set := map[string]bool{}
	for _, p := range open {
		set[itoaPort(p)] = true
	}
	return func(_ context.Context, _, address string) (net.Conn, error) {
		_, port, _ := net.SplitHostPort(address)
		if set[port] {
			return fakeConn{}, nil
		}
		return nil, errors.New("connection refused")
	}
}

func itoaPort(p int) string {
	return netip.AddrPortFrom(netip.MustParseAddr("0.0.0.0"), uint16(p)).String()[len("0.0.0.0:"):]
}

// TestProbeReachability_AnyUp is the core anti-false-positive lock: a device whose
// old monitored ports (3389/445) are dead but whose tcp/5060 is up must be reported
// ONLINE, with the winning signal + the failed candidates as evidence.
func TestProbeReachability_AnyUp(t *testing.T) {
	p := NewPoller(dialOnlyPorts(5060), time.Second)
	rr := p.ProbeReachability(context.Background(), netip.MustParseAddr("150.0.0.190"), []int{3389, 445, 5060})
	if !rr.OK {
		t.Fatalf("device must be OK when any candidate answers; got %+v", rr)
	}
	if rr.Signal != "tcp/5060" {
		t.Errorf("winning signal = %q, want tcp/5060", rr.Signal)
	}
	if strings.Join(rr.Up, ",") != "tcp/5060" {
		t.Errorf("up = %v, want [tcp/5060]", rr.Up)
	}
	if strings.Join(rr.Down, ",") != "tcp/3389,tcp/445" {
		t.Errorf("down = %v, want [tcp/3389 tcp/445]", rr.Down)
	}
}

func TestProbeReachability_AllDown(t *testing.T) {
	p := NewPoller(dialOnlyPorts( /* nothing */ ), time.Second)
	rr := p.ProbeReachability(context.Background(), netip.MustParseAddr("150.0.0.99"), []int{80, 443})
	if rr.OK || rr.Signal != "" {
		t.Fatalf("all-closed host must be down with no signal; got %+v", rr)
	}
	if len(rr.Down) != 2 {
		t.Errorf("down should list both failed candidates; got %v", rr.Down)
	}
}

func TestProbeReachability_MultipleUp(t *testing.T) {
	p := NewPoller(dialOnlyPorts(443, 8080), time.Second)
	rr := p.ProbeReachability(context.Background(), netip.MustParseAddr("10.0.0.1"), []int{443, 8080})
	if !rr.OK || rr.Signal != "tcp/443" || len(rr.Up) != 2 {
		t.Fatalf("two-up host: want first winner tcp/443 + 2 up signals; got %+v", rr)
	}
}

func mkReach(status, signal string, evidence string) db.MonitoringCheck {
	return db.MonitoringCheck{LastStatus: status, LastSignal: signal, LastEvidence: []byte(evidence)}
}

// TestReachabilityEvidence locks the confidence contract: none (nothing up),
// medium (one signal), high (≥2 signals up).
func TestReachabilityEvidence(t *testing.T) {
	// one signal → medium
	sig, conf := reachabilityEvidence([]db.MonitoringCheck{mkReach("up", "tcp/5060", `{"up":["tcp/5060"],"down":["tcp/3389"]}`)})
	if sig != "tcp/5060" || conf != "medium" {
		t.Fatalf("single-signal = (%q,%q), want (tcp/5060, medium)", sig, conf)
	}
	// two signals up → high
	_, conf = reachabilityEvidence([]db.MonitoringCheck{mkReach("up", "tcp/443", `{"up":["tcp/443","tcp/8080"]}`)})
	if conf != "high" {
		t.Fatalf("two-signal confidence = %q, want high", conf)
	}
	// nothing up → none
	sig, conf = reachabilityEvidence([]db.MonitoringCheck{mkReach("down", "", `{"down":["tcp/443"]}`)})
	if sig != "" || conf != "none" {
		t.Fatalf("down = (%q,%q), want ('', none)", sig, conf)
	}
}

func TestEvidenceJSON(t *testing.T) {
	if got := string(evidenceJSON([]string{"tcp/5060"}, []string{"tcp/3389"})); got != `{"down":["tcp/3389"],"up":["tcp/5060"]}` && got != `{"up":["tcp/5060"],"down":["tcp/3389"]}` {
		t.Fatalf("evidenceJSON = %s", got)
	}
	if got := string(evidenceJSON(nil, nil)); got != "{}" {
		t.Fatalf("empty evidenceJSON = %s, want {}", got)
	}
}
