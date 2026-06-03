// Package scan orchestrates a fleet scan: it runs a per-IP discover→apply
// function across a scope with bounded concurrency and aggregates the outcome.
// The per-IP function is injected (the collector supplies one that wires the
// pipeline + apply worker), which keeps this package transport-free and
// unit-testable.
package scan

import (
	"context"
	"net/netip"
	"sync"

	"github.com/google/uuid"
)

// PerIP discovers and persists one IP, returning the device id (uuid.Nil if
// the host was not alive / not enrolled) and an error.
type PerIP func(ctx context.Context, ip netip.Addr) (uuid.UUID, error)

// Result aggregates a scan.
type Result struct {
	Total     int
	Persisted int // device id returned (alive + enrolled)
	Skipped   int // alive=false / not enrolled (nil id, nil error)
	Failed    int // returned an error
}

// Scope runs fn over every IP with at most `concurrency` in flight. Order of
// completion doesn't matter; the aggregate is returned once all finish. A
// cancelled context stops dispatching new work and is reflected as Failed for
// the unstarted remainder is NOT counted — only attempted IPs are tallied.
func Scope(ctx context.Context, ips []netip.Addr, concurrency int, fn PerIP) Result {
	if concurrency < 1 {
		concurrency = 8
	}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	res := Result{}

	for _, ip := range ips {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ip netip.Addr) {
			defer wg.Done()
			defer func() { <-sem }()
			id, err := fn(ctx, ip)
			mu.Lock()
			defer mu.Unlock()
			res.Total++
			switch {
			case err != nil:
				res.Failed++
			case id == uuid.Nil:
				res.Skipped++
			default:
				res.Persisted++
			}
		}(ip)
	}
	wg.Wait()
	return res
}
