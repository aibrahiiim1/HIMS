package api

import (
	"net/http"
	"sort"
	"time"
)

// "Needs attention now" — the consolidated, prioritized list of REAL issues the
// operator should act on, assembled from live data only. It reuses the exact same
// derivations the rest of the app uses (statusDataQualityIssues for management /
// reachability gaps, ListAlerts for alert state) so a count here can never
// contradict the page it links to. An item appears ONLY when its live count > 0,
// so an all-clear system shows an empty list — never a fabricated "everything's on
// fire" or a hardcoded blocker.

type actionItem struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Count       int    `json:"count"`
	Status      string `json:"status"`      // critical | warning | info
	Explanation string `json:"explanation"` // plain-language, why it matters
	ReasonCode  string `json:"reason_code"`
	Route       string `json:"route"` // real drill-down filter
}

// actionRoute maps each management/reachability issue key to the real filtered
// page that resolves it. Missing keys fall back to the Data Quality center.
var actionRoute = map[string]string{
	"online_but_unmanaged":             "/inventory/unmanaged",
	"reachable_but_no_credential":      "/inventory/unmanaged",
	"credential_bound_but_not_working": "/inventory/unmanaged",
	"needs_agent_collection":           "/inventory/unmanaged",
	"agent_offline_for_managed_site":   "/agents",
	"offline_but_previously_managed":   "/inventory?reachability=offline",
	"managed_device_collection_stale":  "/data-quality",
	"collection_not_attempted":         "/inventory/unmanaged",
	"credential_not_authorized":        "/inventory/unmanaged",
	"web_authenticated_no_deep":        "/inventory/unmanaged",
	"inventory_only_offline":           "/inventory?management=inventory_only",
}

func actionSeverityRank(s string) int {
	switch s {
	case "critical":
		return 0
	case "warning":
		return 1
	default: // info
		return 2
	}
}

// dashboardActionRequired (GET /dashboard/action-required) returns the prioritized
// "act now" list plus the list of currently-healthy areas, all from live data.
func (s *Server) dashboardActionRequired(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()

	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	devs = s.scopeDevices(ctx, devs)
	maps, merr := s.buildStatusMaps(ctx)
	if merr != nil {
		writeErr(w, merr)
		return
	}

	items := []actionItem{}

	// (1) Open critical alerts — the highest-signal "act now". Also count open alerts
	// with no check linkage (hygiene) in the same pass.
	criticalAlerts, nullCheck := 0, 0
	if alerts, aerr := s.queries.ListAlerts(ctx); aerr == nil {
		for _, a := range alerts {
			if a.Status == "resolved" {
				continue
			}
			if a.CheckID == nil {
				nullCheck++
			}
			if a.Severity == "critical" {
				criticalAlerts++
			}
		}
	}
	if criticalAlerts > 0 {
		items = append(items, actionItem{
			Key: "critical_alerts", Label: "Critical alerts", Count: criticalAlerts, Status: "critical",
			Explanation: "Open critical alerts — active incidents (outage, hardware fault…) needing attention now.",
			ReasonCode:  "critical_alerts_open", Route: "/alerts",
		})
	}

	// (2) Management + reachability gaps — reuse the SAME buckets the Data Quality /
	// status pages compute, so counts match exactly. Each already carries a plain
	// description + honest severity and is only present when its count > 0.
	for _, iss := range maps.statusDataQualityIssues(devs, now) {
		route := actionRoute[iss.Key]
		if route == "" {
			route = "/data-quality"
		}
		items = append(items, actionItem{
			Key: iss.Key, Label: iss.Label, Count: iss.Count, Status: iss.Severity,
			Explanation: iss.Description, ReasonCode: iss.Key, Route: route,
		})
	}

	// (3) Alert hygiene — orphaned/state alerts with no check linkage. Surfaced, not
	// closed; low priority (info) so it never masks a real incident.
	if nullCheck > 0 {
		items = append(items, actionItem{
			Key: "alert_hygiene", Label: "Alert hygiene", Count: nullCheck, Status: "info",
			Explanation: "Open alerts with no monitoring-check linkage (state-based collection/datastore alerts). They resolve by their own state evaluation, not a check recovery.",
			ReasonCode:  "null_check_id", Route: "/alerts",
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if r := actionSeverityRank(items[i].Status) - actionSeverityRank(items[j].Status); r != 0 {
			return r < 0
		}
		return items[i].Count > items[j].Count
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"updated_at": now.Format(isoFmt),
		"items":      items,
		"all_clear":  len(items) == 0,
	})
}
