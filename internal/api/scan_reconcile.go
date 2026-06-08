package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// reconcileStaleScans fails discovery jobs that are still 'running'/'pending'
// but have no live worker. A scan executes as an in-process goroutine, so a job
// whose start is older than `olderThan` is orphaned — most commonly because the
// API process restarted mid-scan (its goroutine died but the DB row stayed
// 'running' forever). olderThan == 0 fails ALL in-flight jobs (used at startup,
// where by definition no scan goroutine survived). Returns the count reconciled.
func (s *Server) reconcileStaleScans(ctx context.Context, olderThan time.Duration, reason string) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	r := reason
	return s.queries.FailStaleScanJobs(ctx, db.FailStaleScanJobsParams{StartedAt: &cutoff, Error: &r})
}

// StartScanReconciler is the discovery worker-crash-recovery loop. On boot it
// immediately fails any scan left 'running'/'pending' across the restart (no
// goroutine survives a process restart), then on `interval` it fails scans that
// have hung past `maxAge`. Runs until ctx is cancelled.
func (s *Server) StartScanReconciler(ctx context.Context, interval, maxAge time.Duration) {
	if n, err := s.reconcileStaleScans(ctx, 0, "interrupted — the API service restarted while the scan was running"); err != nil {
		slog.Warn("startup scan reconcile failed", "error", err)
	} else if n > 0 {
		slog.Warn("reconciled orphaned scans on startup", "count", n)
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := s.reconcileStaleScans(ctx, maxAge, "timed out — the scan exceeded the maximum run duration"); err == nil && n > 0 {
					slog.Warn("failed stale running scans", "count", n, "older_than", maxAge.String())
				}
			}
		}
	}()
}
