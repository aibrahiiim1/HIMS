package api

import (
	"context"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// defaultAlertRule is one seeded rule. The current alert engine matches MONITORING
// CHECKS by status (down/warning) with an optional device-category filter, so only
// check-status conditions are expressible here. State-based conditions (collection
// stale, agent offline, virtualization-collection failed, datastore low free) are NOT
// check-status and are handled by the device-state evaluator (alert_state.go), not seeded
// as check rules.
type defaultAlertRule struct {
	name          string
	trigger       string  // down | warning
	category      *string // nil = all categories
	severity      string  // info | warning | critical
	minFailures   int32
	escalateAfter int32 // minutes; 0 = never
}

// seedDefaultAlertRules registers the baseline check-status alert rules ONCE, idempotently
// keyed on rule name (existing names are skipped, never duplicated or overwritten — operators
// may freely edit/disable them afterward). Returns how many were newly created.
//
// We seed a single "Device reachability down" rule (all categories, critical, min 2 failures
// to avoid single-probe-blip noise). It already covers ESXi/Hyper-V hosts (they are devices);
// a separate category=virtual_host rule is deliberately NOT seeded because the engine has no
// category-exclusion, so it would double-alert every down hypervisor. min_failures=2 + the
// engine's (rule, check) dedup index means a flapping/down check opens exactly one alert.
func (s *Server) seedDefaultAlertRules(ctx context.Context) (int, error) {
	existing, err := s.queries.ListAlertRules(ctx)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, r := range existing {
		have[r.Name] = true
	}
	defaults := []defaultAlertRule{
		{name: "Device reachability down", trigger: "down", category: nil, severity: "critical", minFailures: 2, escalateAfter: 30},
	}
	created := 0
	for _, d := range defaults {
		if have[d.name] {
			continue
		}
		if _, err := s.queries.CreateAlertRule(ctx, db.CreateAlertRuleParams{
			Name:                 d.name,
			TriggerStatus:        d.trigger,
			MinFailures:          d.minFailures,
			DeviceCategory:       d.category,
			Severity:             d.severity,
			AutoWorkOrder:        false,
			WorkOrderPriority:    "high",
			Enabled:              true,
			EscalateAfterMinutes: d.escalateAfter,
		}); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}
