package api

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Part 2: device-state alert evaluator. The check-status engine (internal/alerting) matches
// monitoring checks; this evaluates device/system STATE (collection staleness, relay-agent
// liveness, virtualization-collection health, datastore free space) that has no monitoring
// check. State alerts carry no check_id and dedup on (rule_id, fingerprint) — the fingerprint
// encodes the condition's identity (device / agent / datastore) so a persistent condition opens
// exactly one alert and auto-resolves when it clears. Runs each sweep after the check engine.

type stateMatch struct {
	deviceID *uuid.UUID
	fp       string
	severity string
	message  string
}

func thr(p *int32, def int32) int32 {
	if p == nil || *p <= 0 {
		return def
	}
	return *p
}
func humanAge(d time.Duration) string {
	h := int(d.Hours())
	if h >= 24 {
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	}
	if h >= 1 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func (s *Server) evaluateStateAlerts(ctx context.Context) (opened, resolved int) {
	rules, err := s.queries.ListEnabledStateRules(ctx)
	if err != nil || len(rules) == 0 {
		return
	}
	var maps *statusMaps
	for _, rule := range rules {
		var matches []stateMatch
		switch rule.Condition {
		case "collection_stale":
			if maps == nil {
				maps, _ = s.buildStatusMaps(ctx)
			}
			matches = s.matchCollectionStale(ctx, rule, maps)
		case "agent_offline":
			matches = s.matchAgentOffline(ctx, rule)
		case "virt_collection":
			matches = s.matchVirtCollection(ctx, rule)
		case "datastore_low":
			matches = s.matchDatastoreLow(ctx, rule)
		default:
			continue
		}
		open, _ := s.queries.ListOpenStateAlertsByRule(ctx, rule.ID)
		openByFP := map[string]uuid.UUID{}
		for _, o := range open {
			openByFP[o.Fingerprint] = o.ID
		}
		matchFP := map[string]bool{}
		for _, m := range matches {
			matchFP[m.fp] = true
			if _, exists := openByFP[m.fp]; exists {
				continue // already open — no duplicate
			}
			a, e := s.queries.OpenStateAlert(ctx, db.OpenStateAlertParams{
				RuleID: rule.ID, DeviceID: m.deviceID, Severity: m.severity, Message: m.message, Fingerprint: m.fp,
			})
			if e != nil {
				continue
			}
			_, _ = s.queries.AddAlertEvent(ctx, db.AddAlertEventParams{AlertID: a.ID, Kind: "opened", Actor: "system", Note: m.message})
			opened++
		}
		// Auto-resolve: any open state alert for this rule whose condition no longer matches.
		for fp, id := range openByFP {
			if matchFP[fp] {
				continue
			}
			if _, e := s.queries.ResolveAlertByID(ctx, id); e == nil {
				_, _ = s.queries.AddAlertEvent(ctx, db.AddAlertEventParams{AlertID: id, Kind: "resolved", Actor: "system", Note: "Auto: condition cleared."})
				resolved++
			}
		}
	}
	return opened, resolved
}

// matchCollectionStale: currently-managed devices whose last successful collection is older than
// the warn/crit age. Only currently-managed devices qualify, so an unknown/never-managed device
// never alerts for staleness.
func (s *Server) matchCollectionStale(ctx context.Context, rule db.AlertRule, maps *statusMaps) []stateMatch {
	warn := time.Duration(thr(rule.WarnThreshold, 24)) * time.Hour
	crit := time.Duration(thr(rule.CritThreshold, 72)) * time.Hour
	rec := map[uuid.UUID]time.Time{}
	if rows, e := s.queries.DeviceCollectionRecency(ctx); e == nil {
		for _, r := range rows {
			rec[r.DeviceID] = r.LastSuccess
		}
	}
	// Collection-staleness is meaningful only for DEEP-collected hosts (authenticated OS /
	// hypervisor inventory). SNMP/ONVIF-managed infra (switch/router/firewall/printer/camera/
	// UPS/AP) is kept fresh by reachability/SNMP checks, not credential collection, so a
	// "collection stale" alert on them is noise — they're excluded here.
	deepCat := map[string]bool{"server": true, "endpoint": true, "virtual_host": true, "virtual_machine": true}
	devs, _ := s.queries.ListAllDevices(ctx)
	var out []stateMatch
	for _, d := range devs {
		if !deepCat[d.Category] {
			continue
		}
		if maps.statusFor(d).Management != MgmtManaged {
			continue
		}
		last, ok := rec[d.ID]
		if !ok || last.Year() < 2000 {
			continue // no successful-collection signal recorded — don't fabricate staleness
		}
		age := time.Since(last)
		if age < warn {
			continue
		}
		sev := "warning"
		if age >= crit {
			sev = "critical"
		}
		id := d.ID
		out = append(out, stateMatch{
			deviceID: &id, fp: "collection_stale:" + d.ID.String(), severity: sev,
			message: fmt.Sprintf("%s%s — managed-device collection stale: last successful collection %s ago", d.Name, ipSuffix(d.PrimaryIp), humanAge(age)),
		})
	}
	return out
}

// matchAgentOffline: enabled relay agents whose last heartbeat is older than the warn minutes
// (or never). Agents are site-scoped (no device_id).
func (s *Server) matchAgentOffline(ctx context.Context, rule db.AlertRule) []stateMatch {
	warn := time.Duration(thr(rule.WarnThreshold, 5)) * time.Minute
	agents, _ := s.queries.ListRelayAgents(ctx)
	var out []stateMatch
	for _, a := range agents {
		if !a.Enabled {
			continue
		}
		hb := "never"
		stale := true
		if a.LastHeartbeat != nil {
			if time.Since(*a.LastHeartbeat) <= warn {
				stale = false
			}
			hb = a.LastHeartbeat.Format("2006-01-02 15:04:05")
		}
		if !stale {
			continue
		}
		out = append(out, stateMatch{
			deviceID: nil, fp: "agent_offline:" + a.ID.String(), severity: orDefault(rule.Severity, "critical"),
			message: fmt.Sprintf("Relay agent %q offline — last heartbeat %s (devices at its site cannot be collected)", a.Name, hb),
		})
	}
	return out
}

// matchVirtCollection: ESXi/Hyper-V hosts whose last virtualization collection failed or is stale.
func (s *Server) matchVirtCollection(ctx context.Context, rule db.AlertRule) []stateMatch {
	staleAfter := time.Duration(thr(rule.WarnThreshold, 24)) * time.Hour
	rows, _ := s.queries.ListAllCollectionHealth(ctx)
	var out []stateMatch
	for _, h := range rows {
		stale := time.Since(h.CollectedAt) > staleAfter
		if h.Status == "ok" && !stale {
			continue
		}
		sev, reason := "warning", "collection stale"
		if h.Status == "failed" {
			sev, reason = "critical", "collection FAILED"
		} else if h.Status != "ok" {
			reason = "collection " + h.Status
		}
		msg := fmt.Sprintf("%s%s — %s %s", h.Name, ipSuffix(h.PrimaryIp), h.Collector, reason)
		if d := derefStr(h.Detail); d != "" {
			msg += ": " + d
		}
		id := h.DeviceID
		out = append(out, stateMatch{deviceID: &id, fp: "virt_collection:" + id.String() + ":" + h.Collector, severity: sev, message: msg})
	}
	return out
}

// matchDatastoreLow: ESXi datastores below the warn/crit free-space percentage.
func (s *Server) matchDatastoreLow(ctx context.Context, rule db.AlertRule) []stateMatch {
	warn := float64(thr(rule.WarnThreshold, 20))
	crit := float64(thr(rule.CritThreshold, 10))
	rows, _ := s.queries.ListAllDatastores(ctx)
	var out []stateMatch
	for _, ds := range rows {
		cap := deref64(ds.CapacityBytes)
		if cap <= 0 {
			continue
		}
		freePct := float64(deref64(ds.FreeBytes)) / float64(cap) * 100
		if freePct >= warn {
			continue
		}
		sev := "warning"
		if freePct < crit {
			sev = "critical"
		}
		id := ds.HostDeviceID
		out = append(out, stateMatch{
			deviceID: &id, fp: "datastore_low:" + id.String() + ":" + ds.Name, severity: sev,
			message: fmt.Sprintf("%s%s — datastore %q low on space: %.0f%% free (warn <%.0f%%)", ds.HostName, ipSuffix(ds.PrimaryIp), ds.Name, freePct, warn),
		})
	}
	return out
}

func ipSuffix(a *netip.Addr) string {
	if a == nil || !a.IsValid() {
		return ""
	}
	return " (" + a.String() + ")"
}
