package api

import (
	"context"
	"encoding/json"

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
		_ = s.queries.ResolveAlertsForDeviceTCPChecks(ctx, d.ID) // avoid orphaning their alerts
		_ = s.queries.DeleteDeviceReachabilityChecks(ctx, d.ID)  // drop a doomed TCP check
		oid := monitoring.SysUpTimeOID
		_, _ = s.queries.UpsertMonitoringCheck(ctx, db.UpsertMonitoringCheckParams{
			DeviceID: d.ID, Kind: "snmp", Oid: &oid, IntervalSeconds: 60, DownThreshold: 2, Enabled: true,
		})
		return
	}

	// Multi-signal reachability: the check is UP if ANY discovered open port answers,
	// so one dead service never flips a live host offline. Ensure a TCP reachability
	// check exists (keep its id/counters if it already targets an open port) and ALWAYS
	// refresh its candidate set to the CURRENT open ports.
	open := make(map[int32]bool, len(openPorts))
	for _, p := range openPorts {
		open[int32(p)] = true
	}
	var check *db.MonitoringCheck
	for i := range existing {
		if existing[i].Kind == "tcp" && existing[i].Role != "supplemental" {
			check = &existing[i]
			break
		}
	}
	port := int32(monitoring.ReachabilityPort(d.Category, d.OsFamily, openPorts))
	if check == nil || check.TargetPort == nil || !open[*check.TargetPort] {
		_ = s.queries.ResolveAlertsForDeviceTCPChecks(ctx, d.ID)
		_ = s.queries.DeleteDeviceReachabilityChecks(ctx, d.ID)
		ch, err := s.queries.UpsertMonitoringCheck(ctx, db.UpsertMonitoringCheckParams{
			DeviceID: d.ID, Kind: "tcp", TargetPort: &port, IntervalSeconds: 60, DownThreshold: 2, Enabled: true,
		})
		if err != nil {
			return
		}
		check = &ch
	}
	blob, _ := json.Marshal(openPorts)
	_ = s.queries.SetCheckCandidatePorts(ctx, db.SetCheckCandidatePortsParams{ID: check.ID, CandidatePorts: blob})
}
