package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/coralsearesorts/hims/internal/domain"
)

// StartBMCHealthRefresh periodically re-collects HPE iLO / BMC identity + overall
// hardware health over SNMP using each controller's BOUND SNMP credential, so the
// fleet's iLO health stays current without an operator pressing "Collect". It mirrors
// the collection self-heal sweep: runs once on startup, then every interval, and is
// bound to ctx so it stops on shutdown.
//
// Hard guarantees (the operator's auto-refresh rules, enforced here + downstream):
//   - BOUND SNMP credential ONLY: collectILOviaSNMP → snmpClientForDevice(community="")
//     reads the device's own bound v2c community or errors. It never tries other
//     communities — no credential spray, no lockout risk.
//   - NO Redfish fallback: this path never touches redfish_status / bmc_info / a
//     managed-via-Redfish state. Redfish inventory still needs a real Redfish credential.
//   - NEVER clobbers proven identity: UpdateDeviceHardwareInfo COALESCEs blank reads, and
//     collectILOviaSNMP writes nothing at all when the device returns no CPQ identity — a
//     failed/weak read can only leave the row as-is, never wipe it.
//   - SNMP failure is a silent no-op: it leaves the prior bmc.snmp_health fact (and its
//     observed_at age, which Data Quality reads to show staleness) untouched. It is NEVER
//     recorded as credential_failed — only a proven SNMP auth rejection would be, and this
//     read path makes no such claim. Honest state: stale, not "auth failed".
//   - Health flows through EXISTING surfaces: the stored bmc.snmp_health fact is what the
//     BMC inventory page and the "Server hardware health (iLO)" Data Quality check already
//     read. This sweep opens no new alert channel, so a Failed/Degraded server is surfaced
//     once, where it belongs — not paged on every tick.
func (s *Server) StartBMCHealthRefresh(ctx context.Context, interval time.Duration) {
	sweep := func() {
		// Detach from any request lifecycle; this is a background maintenance pass.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		devs, err := s.queries.ListDevicesByCategory(sctx, string(domain.CatBMC))
		if err != nil {
			slog.Warn("bmc health refresh sweep failed", "error", err)
			return
		}
		refreshed, skipped := 0, 0
		for _, dev := range devs {
			// Eligible = an out-of-band controller with an operator-BOUND credential and a
			// real IP. A BMC with no bound credential is skipped entirely (no probe, no
			// spray); it stays honestly un-refreshed until a credential is bound.
			if dev.CredentialID == nil || dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
				skipped++
				continue
			}
			if s.collectILOviaSNMP(sctx, dev) {
				refreshed++
			}
		}
		if refreshed > 0 {
			slog.Info("bmc health refresh re-collected iLO identity/health over SNMP",
				"refreshed", refreshed, "candidates", len(devs), "skipped_no_cred", skipped)
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
