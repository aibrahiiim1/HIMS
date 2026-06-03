package scan

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ips(n int) []netip.Addr {
	out := make([]netip.Addr, n)
	a := netip.MustParseAddr("10.0.0.1")
	for i := 0; i < n; i++ {
		out[i] = a
		a = a.Next()
	}
	return out
}

func TestScope_AggregatesOutcomes(t *testing.T) {
	// 10 IPs: even index persists, index%3==0 (and odd) fails, rest skip.
	fn := func(_ context.Context, ip netip.Addr) (uuid.UUID, error) {
		last := ip.As4()[3]
		switch {
		case last%2 == 0:
			return uuid.New(), nil // persisted
		case last%5 == 0:
			return uuid.Nil, errors.New("boom") // failed
		default:
			return uuid.Nil, nil // skipped
		}
	}
	res := Scope(context.Background(), ips(10), 4, fn)
	if res.Total != 10 {
		t.Fatalf("total = %d; want 10", res.Total)
	}
	if res.Persisted+res.Skipped+res.Failed != 10 {
		t.Fatalf("buckets don't sum to total: %+v", res)
	}
	if res.Persisted == 0 || res.Failed == 0 {
		t.Fatalf("expected some persisted + some failed: %+v", res)
	}
}

func TestScope_RespectsConcurrencyLimit(t *testing.T) {
	var inflight, maxSeen int64
	fn := func(_ context.Context, _ netip.Addr) (uuid.UUID, error) {
		n := atomic.AddInt64(&inflight, 1)
		for {
			m := atomic.LoadInt64(&maxSeen)
			if n <= m || atomic.CompareAndSwapInt64(&maxSeen, m, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt64(&inflight, -1)
		return uuid.New(), nil
	}
	Scope(context.Background(), ips(50), 5, fn)
	if maxSeen > 5 {
		t.Fatalf("max in-flight = %d; exceeds concurrency 5", maxSeen)
	}
}

func TestScope_CancelStopsDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	var called int64
	fn := func(_ context.Context, _ netip.Addr) (uuid.UUID, error) {
		atomic.AddInt64(&called, 1)
		return uuid.New(), nil
	}
	res := Scope(ctx, ips(100), 4, fn)
	if called > 0 || res.Total > 0 {
		t.Fatalf("cancelled scan should dispatch nothing; called=%d total=%d", called, res.Total)
	}
}
