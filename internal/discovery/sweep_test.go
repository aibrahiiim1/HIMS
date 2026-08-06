package discovery

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

// listenOn starts a TCP listener on 127.0.0.1 and returns its port.
func listenOn(t *testing.T) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, ps, _ := net.SplitHostPort(l.Addr().String())
	p, _ := strconv.Atoi(ps)
	return p, func() { _ = l.Close() }
}

func TestControlsFor(t *testing.T) {
	cases := []struct {
		cidr            string
		wantNet, wantBc string
	}{
		{"172.21.96.0/24", "172.21.96.0", "172.21.96.255"},
		{"10.0.0.0/30", "10.0.0.0", "10.0.0.3"},
		{"192.168.1.128/25", "192.168.1.128", "192.168.1.255"},
	}
	for _, c := range cases {
		p := netip.MustParsePrefix(c.cidr)
		got := ControlsFor(p)
		if len(got) != 2 {
			t.Fatalf("%s: want 2 controls, got %d", c.cidr, len(got))
		}
		if got[0].String() != c.wantNet || got[1].String() != c.wantBc {
			t.Errorf("%s: got %v/%v want %s/%s", c.cidr, got[0], got[1], c.wantNet, c.wantBc)
		}
	}
	// /31 and /32 have no unusable ends — no controls available.
	for _, c := range []string{"10.0.0.0/31", "10.0.0.1/32"} {
		if got := ControlsFor(netip.MustParsePrefix(c)); got != nil {
			t.Errorf("%s: want no controls, got %v", c, got)
		}
	}
}

// The regression this whole file exists for: a port answering on a control
// address must not make every scanned address look alive.
func TestLivenessSweep_ControlProvesPromiscuousPort(t *testing.T) {
	algPort, stop := listenOn(t)
	defer stop()

	// Every address here is 127.0.0.1, so the "ALG" port answers for all of
	// them AND for the control — exactly the shape of a SIP ALG on a subnet.
	lo := netip.MustParseAddr("127.0.0.1")
	hosts := []netip.Addr{lo, lo, lo}
	cfg := SweepConfig{
		Ports:    []int{algPort},
		Timeout:  time.Second,
		Controls: []netip.Addr{lo}, // stands in for the network address
	}
	res := LivenessSweep(context.Background(), hosts, cfg, nil)

	if !res.IsPromiscuous(algPort) {
		t.Fatalf("port %d answered on the control address and must be flagged promiscuous; promiscuous=%v", algPort, res.Promiscuous)
	}
	if len(res.Alive) != 0 {
		t.Errorf("no address had trustworthy evidence, want 0 alive, got %d", len(res.Alive))
	}
	if len(res.SuppressedByMiddlebox) != len(hosts) {
		t.Errorf("want all %d addresses reported as suppressed (not silently dropped), got %d", len(hosts), len(res.SuppressedByMiddlebox))
	}
	if !res.Promiscuous[0].ViaControl {
		t.Error("detection came from a control address, so ViaControl must be true")
	}
}

// A port that answers only on real hosts must stay trusted.
func TestLivenessSweep_GenuinePortStaysTrusted(t *testing.T) {
	realPort, stop := listenOn(t)
	defer stop()

	lo := netip.MustParseAddr("127.0.0.1")
	// Control is an address with nothing listening (RFC5737 test net, no route
	// needed for a connect refusal/timeout within the timeout budget).
	dead := netip.MustParseAddr("192.0.2.1")
	cfg := SweepConfig{
		Ports:    []int{realPort},
		Timeout:  300 * time.Millisecond,
		Controls: []netip.Addr{dead},
		// Sample is tiny; keep the rate heuristic out of this test.
		MinSampleForRate: 1000,
	}
	res := LivenessSweep(context.Background(), []netip.Addr{lo}, cfg, nil)

	if res.IsPromiscuous(realPort) {
		t.Errorf("port %d did not answer on the control and must stay trusted", realPort)
	}
	if len(res.Alive) != 1 {
		t.Errorf("the real host must be alive, got alive=%v suppressed=%v noresp=%v", res.Alive, res.SuppressedByMiddlebox, res.NoResponse)
	}
}

// With no controls available (target-list scan), a near-100% open rate over a
// large enough sample still flags the port.
func TestLivenessSweep_RateHeuristicWithoutControls(t *testing.T) {
	algPort, stop := listenOn(t)
	defer stop()

	lo := netip.MustParseAddr("127.0.0.1")
	hosts := make([]netip.Addr, 40)
	for i := range hosts {
		hosts[i] = lo
	}
	cfg := SweepConfig{
		Ports:            []int{algPort},
		Timeout:          time.Second,
		PromiscuousRate:  0.95,
		MinSampleForRate: 32,
	}
	res := LivenessSweep(context.Background(), hosts, cfg, nil)
	if !res.IsPromiscuous(algPort) {
		t.Fatalf("100%% open rate over %d addresses must be flagged", len(hosts))
	}
	if res.Promiscuous[0].ViaControl {
		t.Error("no control was supplied, so ViaControl must be false")
	}
}

// Below the minimum sample a fully-populated small range must NOT be flagged —
// six cameras on a /29 all serving :80 is legitimate.
func TestLivenessSweep_SmallFullRangeNotFlagged(t *testing.T) {
	port, stop := listenOn(t)
	defer stop()

	lo := netip.MustParseAddr("127.0.0.1")
	hosts := []netip.Addr{lo, lo, lo, lo, lo, lo}
	cfg := SweepConfig{Ports: []int{port}, Timeout: time.Second, MinSampleForRate: 32}
	res := LivenessSweep(context.Background(), hosts, cfg, nil)
	if res.IsPromiscuous(port) {
		t.Error("a small fully-populated range is plausible and must not be flagged")
	}
	if len(res.Alive) != len(hosts) {
		t.Errorf("want all %d alive, got %d", len(hosts), len(res.Alive))
	}
}

// An address the operator NAMED must not be silently dropped just because the
// only port it answers on is one a middlebox serves. Sweeping a /24 and finding
// such an address is not evidence of a device; typing that address is the
// operator asserting one exists, and answering "nothing found" ignores a direct
// instruction. It is admitted, and reported as liveness-unproven.
func TestLivenessSweep_AssertedAddressSurvivesSuppression(t *testing.T) {
	algPort, stop := listenOn(t)
	defer stop()

	lo := netip.MustParseAddr("127.0.0.1")
	cfg := SweepConfig{
		Ports:    []int{algPort},
		Timeout:  time.Second,
		Controls: []netip.Addr{lo}, // control answers => port is promiscuous
		Asserted: map[netip.Addr]bool{lo: true},
	}
	res := LivenessSweep(context.Background(), []netip.Addr{lo}, cfg, nil)

	if !res.IsPromiscuous(algPort) {
		t.Fatalf("port %d answered on the control and must still be flagged promiscuous", algPort)
	}
	if len(res.Alive) != 1 {
		t.Errorf("an asserted address must be admitted, got alive=%v suppressed=%v", res.Alive, res.SuppressedByMiddlebox)
	}
	if len(res.SuppressedByMiddlebox) != 0 {
		t.Errorf("an asserted address must not be suppressed, got %v", res.SuppressedByMiddlebox)
	}
	if len(res.LivenessUnproven) != 1 {
		t.Errorf("the weak evidence must be reported, want 1 unproven, got %v", res.LivenessUnproven)
	}
	if !strings.Contains(res.Summary(), "liveness unproven") {
		t.Errorf("summary must disclose the unproven liveness, got: %s", res.Summary())
	}
}

// The same evidence WITHOUT an assertion must still be suppressed — that is what
// stopped 193 phantom enrolments on the production /24.
func TestLivenessSweep_UnassertedAddressStillSuppressed(t *testing.T) {
	algPort, stop := listenOn(t)
	defer stop()

	lo := netip.MustParseAddr("127.0.0.1")
	res := LivenessSweep(context.Background(), []netip.Addr{lo}, SweepConfig{
		Ports:    []int{algPort},
		Timeout:  time.Second,
		Controls: []netip.Addr{lo},
		// no Asserted
	}, nil)
	if len(res.Alive) != 0 {
		t.Errorf("without an assertion the address must stay suppressed, got alive=%v", res.Alive)
	}
	if len(res.LivenessUnproven) != 0 {
		t.Errorf("nothing was asserted, so nothing should be reported unproven, got %v", res.LivenessUnproven)
	}
}
