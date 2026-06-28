package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Self-heal for terminal TRANSIENT collection failures. Layers 1 (retry envelope) and
// 2 (load governor) keep most hosts collecting through a from-zero storm, but a host
// can still exhaust its retry budget while load is at peak and settle terminally
// 'failed' with a transient category (winrm_negotiate_error / winrm_connect_timeout /
// agent_no_result). There is no other path that re-collects a terminally-failed host,
// so without this sweep a reachable, correctly-credentialed host stays
// collection_failed until the operator runs another scan. The sweep re-collects those
// — and ONLY those — once the storm has passed.
//
// Hard guarantees (enforced by ListSelfHealCandidates):
//   - Never re-collects an AUTH/authz failure (operator must fix the credential) — no
//     credential re-spray, no lockout risk.
//   - Never re-collects a host that already has os_inventory evidence (it is managed).
//   - Skips hosts with a collect_os job already in flight (no duplicate work).
//   - Waits selfHealCooldown after the failure so it never re-storms a busy scan.
//   - Bounded: a host that has burned selfHealMaxRounds failed transient jobs in 24h
//     is left alone (honest collection_failed) instead of being retried forever.
const (
	// selfHealCooldownMins: how long a terminal transient failure must sit before the
	// sweep re-collects it — long enough for a from-zero collection storm to drain so
	// the re-collection runs against recovered listeners, not the storm that caused it.
	selfHealCooldownMins = 15
	// selfHealMaxRounds: max failed transient collect_os jobs for a device in 24h before
	// self-heal gives up (so a genuinely unreachable host is not retried indefinitely).
	selfHealMaxRounds = 4
)

// StartCollectionSelfHeal periodically re-collects hosts stranded in a terminal
// transient collection failure (see file doc). Runs once on startup, then every
// interval; bound to ctx so it stops on shutdown.
func (s *Server) StartCollectionSelfHeal(ctx context.Context, interval time.Duration) {
	sweep := func() {
		// Detach from any request lifecycle; this is a background maintenance pass.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		cands, err := s.queries.ListSelfHealCandidates(sctx, db.ListSelfHealCandidatesParams{
			Column1: selfHealCooldownMins, Column2: selfHealMaxRounds,
		})
		if err != nil {
			slog.Warn("collection self-heal sweep failed", "error", err)
			return
		}
		healed := 0
		for _, c := range cands {
			dev, derr := s.queries.GetDevice(sctx, c.ID)
			if derr != nil {
				continue
			}
			// Reuse the exact same routing the scan uses — it re-checks the in-flight
			// dedup and the site agent, and enqueues a fresh collect_os job (which gets
			// the full max_attempts=5 retry envelope again). winrm = the agent's
			// WinRM-shell-first Windows ladder.
			if _, ok := s.routeViaSiteAgent(sctx, dev, c.Ip, "winrm"); ok {
				healed++
			}
		}
		if healed > 0 {
			slog.Info("collection self-heal re-collected stranded transient failures",
				"count", healed, "candidates", len(cands))
		}

		// Enqueue-gap reconciler: a reachable Windows-like host can land in inventory
		// with NO collect_os job EVER (a from-zero scan that couldn't enqueue — agent
		// briefly offline, a routing race, a dispatch miss). Self-heal above only
		// re-runs FAILED jobs, so such a host stays "not_attempted" forever. This pass
		// gives each one its FIRST attempt via the same site-agent routing the scan
		// uses. windowsLike() is re-checked in Go so only agent-collectable hosts are
		// routed; routeViaSiteAgent dedups + needs a site agent, and a host leaves this
		// set once it has any job — so at most one reconciler attempt per host (no
		// credential spray, no loop). This permanently closes the enqueue gap for all
		// future scans, not just the current one.
		gapCands, gerr := s.queries.ListNeverAttemptedAgentCandidates(sctx)
		if gerr != nil {
			slog.Warn("never-attempted reconciler query failed", "error", gerr)
			return
		}
		enqueued := 0
		for _, c := range gapCands {
			dev, derr := s.queries.GetDevice(sctx, c.ID)
			if derr != nil || !windowsLike(dev) {
				continue
			}
			if _, ok := s.routeViaSiteAgent(sctx, dev, c.Ip, "winrm"); ok {
				enqueued++
			}
		}
		if enqueued > 0 {
			slog.Info("collection reconciler enqueued first attempt for never-attempted hosts",
				"count", enqueued, "candidates", len(gapCands))
		}
	}
	sweep()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweep()
			}
		}
	}()
}
