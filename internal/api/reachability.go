package api

import (
	"context"

	"github.com/coralsearesorts/hims/internal/monitoring"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// seedReachabilityCheck ensures a freshly discovered/up host has a reachability
// monitoring check that actually reflects its liveness, instead of a hardcoded
// category/default port the host may not serve (which marked up hosts "down").
//
//   - If the host answered on TCP ports, the check dials a port from that set
//     (monitoring.ReachabilityPort), replacing any stale TCP check that points
//     at a port the host doesn't serve.
//   - If the host answered only via SNMP (no open TCP ports), it gets an SNMP
//     liveness check (sysUpTime) rather than a TCP dial that would always fail.
//   - A correct check already on a confirmed-open port (incl. an operator's) is
//     left untouched.
//
// Best-effort: monitoring is non-critical to enrollment, so errors are ignored.
func (s *Server) seedReachabilityCheck(ctx context.Context, d db.Device, openPorts []int, snmpAlive bool) {
	existing, _ := s.queries.ListMonitoringChecksByDevice(ctx, d.ID)

	if len(openPorts) == 0 {
		// No open TCP ports. If SNMP answered, use an SNMP liveness check.
		if !snmpAlive {
			return // not TCP-alive and no SNMP — leave whatever default exists
		}
		for _, c := range existing {
			if c.Kind == "snmp" {
				return // already has an SNMP check
			}
		}
		_ = s.queries.DeleteDeviceReachabilityChecks(ctx, d.ID) // drop a doomed TCP check
		oid := monitoring.SysUpTimeOID
		_, _ = s.queries.UpsertMonitoringCheck(ctx, db.UpsertMonitoringCheckParams{
			DeviceID: d.ID, Kind: "snmp", Oid: &oid, IntervalSeconds: 60, DownThreshold: 2, Enabled: true,
		})
		return
	}

	// Keep an existing TCP check only if it already targets a confirmed-open port.
	open := make(map[int32]bool, len(openPorts))
	for _, p := range openPorts {
		open[int32(p)] = true
	}
	for _, c := range existing {
		if c.Kind == "tcp" && c.TargetPort != nil && open[*c.TargetPort] {
			return
		}
	}
	// Replace any stale TCP check with an evidence-based one.
	_ = s.queries.DeleteDeviceReachabilityChecks(ctx, d.ID)
	port := int32(monitoring.ReachabilityPort(d.Category, d.OsFamily, openPorts))
	_, _ = s.queries.UpsertMonitoringCheck(ctx, db.UpsertMonitoringCheckParams{
		DeviceID: d.ID, Kind: "tcp", TargetPort: &port, IntervalSeconds: 60, DownThreshold: 2, Enabled: true,
	})
}
