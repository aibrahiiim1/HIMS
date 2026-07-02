package api

import (
	"sort"
	"testing"
)

// The "needs attention now" list must sort worst-first: critical before warning
// before info, and within a severity the largest count first — so the operator's
// eye lands on the biggest real fire.
func TestActionRequired_Prioritization(t *testing.T) {
	items := []actionItem{
		{Key: "hygiene", Status: "info", Count: 27},
		{Key: "needs_cred", Status: "warning", Count: 3},
		{Key: "offline", Status: "warning", Count: 9},
		{Key: "critical_alerts", Status: "critical", Count: 1},
	}
	sort.SliceStable(items, func(i, j int) bool {
		if r := actionSeverityRank(items[i].Status) - actionSeverityRank(items[j].Status); r != 0 {
			return r < 0
		}
		return items[i].Count > items[j].Count
	})
	want := []string{"critical_alerts", "offline", "needs_cred", "hygiene"}
	for i, w := range want {
		if items[i].Key != w {
			t.Fatalf("position %d: got %q, want %q (order=%v)", i, items[i].Key, w, actionKeys(items))
		}
	}
}

// Every management/reachability issue key the status pages emit must have a real
// drill-down route (no dead action rows). Missing keys fall back to Data Quality,
// which is still a real page — asserted here so a new bucket never routes nowhere.
func TestActionRequired_RoutesResolve(t *testing.T) {
	for _, k := range []string{
		"online_but_unmanaged", "reachable_but_no_credential", "credential_bound_but_not_working",
		"needs_agent_collection", "agent_offline_for_managed_site", "offline_but_previously_managed",
		"collection_not_attempted", "credential_not_authorized", "inventory_only_offline",
	} {
		if actionRoute[k] == "" {
			t.Errorf("issue key %q has no drill-down route", k)
		}
	}
	if actionSeverityRank("critical") >= actionSeverityRank("warning") || actionSeverityRank("warning") >= actionSeverityRank("info") {
		t.Fatal("severity rank must be critical < warning < info")
	}
}

func actionKeys(items []actionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key
	}
	return out
}
