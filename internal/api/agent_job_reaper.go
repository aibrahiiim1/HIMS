package api

import (
	"context"
	"log/slog"
	"time"
)

// StartAgentJobReaper periodically recovers relay-agent collect_os jobs stuck in
// 'dispatched' — handed to an agent that then crashed or dropped the result
// connection and never reported back. Without this, orphaned dispatched jobs would
// wedge BOTH the per-agent dispatch budget (CountDispatchedAgentJobs, which gates
// how much new work an agent receives) and the per-device in-flight dedup
// (CountActiveDeviceAgentJobs, which blocks re-enqueue) forever — so a single agent
// crash mid-scan could permanently stall collection. The reaper requeues a stale
// job (bumped attempt) while attempts remain, else fails it with an honest reason
// so the device settles to a real state instead of pending forever. Runs once on
// startup, then every interval; bound to ctx so it stops on shutdown.
func (s *Server) StartAgentJobReaper(ctx context.Context, interval time.Duration) {
	reap := func() {
		cutoff := time.Now().Add(-staleDispatchedAfter)
		if n, err := s.queries.RequeueStaleAgentJobs(ctx, &cutoff); err != nil {
			slog.Warn("agent job reaper failed", "error", err)
		} else if n > 0 {
			slog.Warn("recovered stale dispatched agent jobs", "count", n, "stale_after", staleDispatchedAfter.String())
		}
	}
	reap()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				reap()
			}
		}
	}()
}
