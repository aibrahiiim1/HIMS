package monitoring

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/coralsearesorts/hims/internal/snmp"
)

// fakeSNMP is an in-memory snmp.Client for ProbeSNMP tests.
type fakeSNMP struct {
	connectErr error
	getErr     error
	value      any
}

func (f *fakeSNMP) Connect(context.Context) error { return f.connectErr }
func (f *fakeSNMP) Get(_ context.Context, oids ...string) ([]snmp.PDU, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return []snmp.PDU{{OID: oids[0], Value: f.value}}, nil
}
func (f *fakeSNMP) BulkWalk(context.Context, string, snmp.WalkFunc) error { return nil }
func (f *fakeSNMP) Walk(context.Context, string, snmp.WalkFunc) error     { return nil }
func (f *fakeSNMP) Close() error                                          { return nil }

// withFakeSNMP swaps the client factory for the duration of a test.
func withFakeSNMP(t *testing.T, c snmp.Client, err error) {
	t.Helper()
	orig := snmpClientFactory
	snmpClientFactory = func(snmp.Target) (snmp.Client, error) { return c, err }
	t.Cleanup(func() { snmpClientFactory = orig })
}

func TestProbeSNMP_Success(t *testing.T) {
	withFakeSNMP(t, &fakeSNMP{value: uint64(123456)}, nil)
	p := NewPoller(nil, time.Second)
	r := p.ProbeSNMP(context.Background(), netip.MustParseAddr("10.0.0.5"), 161, "community", "")
	if !r.OK || r.Err != nil {
		t.Fatalf("ProbeSNMP = %+v; want OK", r)
	}
	if r.Value == nil || *r.Value != 123456 {
		t.Fatalf("value = %v; want 123456", r.Value)
	}
}

func TestProbeSNMP_GetError(t *testing.T) {
	withFakeSNMP(t, &fakeSNMP{getErr: snmp.ErrTimeout}, nil)
	p := NewPoller(nil, time.Second)
	r := p.ProbeSNMP(context.Background(), netip.MustParseAddr("10.0.0.5"), 161, "community", "1.2.3")
	if r.OK {
		t.Fatalf("ProbeSNMP OK on get error; want failure")
	}
}

// fakeConn is a no-op net.Conn; only Close is exercised by the poller.
type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

func TestProbeTCP_Success(t *testing.T) {
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("network = %q; want tcp", network)
		}
		if address != "10.0.0.5:443" {
			t.Fatalf("address = %q; want 10.0.0.5:443", address)
		}
		return fakeConn{}, nil
	}
	p := NewPoller(dial, time.Second)
	r := p.ProbeTCP(context.Background(), netip.MustParseAddr("10.0.0.5"), 443)
	if !r.OK || r.Err != nil {
		t.Fatalf("ProbeTCP = %+v; want OK", r)
	}
}

func TestProbeTCP_Failure(t *testing.T) {
	wantErr := errors.New("connection refused")
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, wantErr }
	p := NewPoller(dial, time.Second)
	r := p.ProbeTCP(context.Background(), netip.MustParseAddr("10.0.0.9"), 22)
	if r.OK {
		t.Fatalf("ProbeTCP OK on dial error; want failure")
	}
	if !errors.Is(r.Err, wantErr) {
		t.Fatalf("err = %v; want %v", r.Err, wantErr)
	}
}

func TestProbeTCP_InvalidAddr(t *testing.T) {
	p := NewPoller(nil, time.Second)
	r := p.ProbeTCP(context.Background(), netip.Addr{}, 80)
	if r.OK || r.Err == nil {
		t.Fatalf("invalid addr should fail; got %+v", r)
	}
}

func TestDefaultPort(t *testing.T) {
	if got := DefaultPort("firewall"); got != 443 {
		t.Errorf("firewall port = %d; want 443", got)
	}
	if got := DefaultPort("switch"); got != 22 {
		t.Errorf("switch port = %d; want 22", got)
	}
	if got := DefaultPort("something_unmapped"); got != 443 {
		t.Errorf("unmapped fallback = %d; want 443", got)
	}
}
