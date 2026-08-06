package api

import (
	"strings"
	"testing"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// A three-level tree mirroring production: group → hotel → building.
type scopeFixture struct {
	group, hotelA, hotelB, bldgA1 uuid.UUID
	parents                       map[uuid.UUID]uuid.UUID
	locs                          []db.Location
}

func newScopeFixture() scopeFixture {
	f := scopeFixture{group: uuid.New(), hotelA: uuid.New(), hotelB: uuid.New(), bldgA1: uuid.New()}
	f.parents = map[uuid.UUID]uuid.UUID{
		f.hotelA: f.group,
		f.hotelB: f.group,
		f.bldgA1: f.hotelA,
	}
	f.locs = []db.Location{
		{ID: f.group, Name: "Coral Sea Group"},
		{ID: f.hotelA, Name: "CHR", ParentID: &f.group},
		{ID: f.hotelB, Name: "CAC", ParentID: &f.group},
		{ID: f.bldgA1, Name: "CHR-B1", ParentID: &f.hotelA},
	}
	return f
}

func agentAt(name string, loc uuid.UUID, includeDesc bool) db.RelayAgent {
	l := loc
	hb := time.Now().UTC()
	return db.RelayAgent{
		ID: uuid.New(), Name: name, LocationID: &l, Enabled: true,
		IncludeDescendants: includeDesc, Status: "online", LastHeartbeat: &hb,
	}
}

// --- 1. Exact matching remains the default -----------------------------------

func TestPickSiteAgent_ExactMatch(t *testing.T) {
	f := newScopeFixture()
	a := agentAt("chr-agent", f.hotelA, false)
	m, amb, ok := pickSiteAgent([]db.RelayAgent{a}, f.parents, f.hotelA, nil)
	if !ok || amb != nil {
		t.Fatalf("want a clean exact match, got ok=%v ambiguous=%v", ok, amb)
	}
	if m.Kind != agentMatchExact || m.Agent.Name != "chr-agent" || m.Depth != 0 {
		t.Errorf("got kind=%s agent=%s depth=%d, want exact/chr-agent/0", m.Kind, m.Agent.Name, m.Depth)
	}
}

// --- 2. Existing agents keep their behaviour: no inheritance unless enabled ---

func TestPickSiteAgent_ParentWithoutInheritanceServesNothing(t *testing.T) {
	f := newScopeFixture()
	// A group-level agent with the flag OFF — the pre-upgrade state of every
	// existing agent. It must NOT pick up the child hotel.
	group := agentAt("group-agent", f.group, false)
	_, amb, ok := pickSiteAgent([]db.RelayAgent{group}, f.parents, f.hotelA, nil)
	if ok {
		t.Error("a parent agent without include_descendants must not serve a child site")
	}
	if amb != nil {
		t.Errorf("this is a no-match, not an ambiguity; got %v", amb)
	}
}

// --- 3. Enabled inheritance --------------------------------------------------

func TestPickSiteAgent_InheritedWhenEnabled(t *testing.T) {
	f := newScopeFixture()
	group := agentAt("group-agent", f.group, true)
	m, amb, ok := pickSiteAgent([]db.RelayAgent{group}, f.parents, f.hotelA, nil)
	if !ok || amb != nil {
		t.Fatalf("want an inherited match, got ok=%v ambiguous=%v", ok, amb)
	}
	if m.Kind != agentMatchInherited || m.Via != f.group || m.Depth != 1 {
		t.Errorf("got kind=%s via=%v depth=%d, want inherited/group/1", m.Kind, m.Via, m.Depth)
	}
}

// Inheritance reaches through more than one level, and reports the real depth.
func TestPickSiteAgent_InheritedTwoLevels(t *testing.T) {
	f := newScopeFixture()
	group := agentAt("group-agent", f.group, true)
	m, _, ok := pickSiteAgent([]db.RelayAgent{group}, f.parents, f.bldgA1, nil)
	if !ok || m.Depth != 2 || m.Kind != agentMatchInherited {
		t.Errorf("a grandchild site must inherit from the group; got ok=%v depth=%d kind=%s", ok, m.Depth, m.Kind)
	}
}

// --- 4. Child-site precedence ------------------------------------------------

func TestPickSiteAgent_DirectChildAgentBeatsInheritedParent(t *testing.T) {
	f := newScopeFixture()
	group := agentAt("group-agent", f.group, true)
	child := agentAt("chr-agent", f.hotelA, false)
	m, amb, ok := pickSiteAgent([]db.RelayAgent{group, child}, f.parents, f.hotelA, nil)
	if !ok || amb != nil {
		t.Fatalf("want a clean pick, got ok=%v ambiguous=%v", ok, amb)
	}
	if m.Agent.Name != "chr-agent" || m.Kind != agentMatchExact {
		t.Errorf("a directly assigned agent must win over an inherited parent; got %s (%s)", m.Agent.Name, m.Kind)
	}
}

// The nearer ancestor wins over a farther one.
func TestPickSiteAgent_NearestAncestorWins(t *testing.T) {
	f := newScopeFixture()
	group := agentAt("group-agent", f.group, true)
	hotel := agentAt("hotel-agent", f.hotelA, true)
	m, _, ok := pickSiteAgent([]db.RelayAgent{group, hotel}, f.parents, f.bldgA1, nil)
	if !ok || m.Agent.Name != "hotel-agent" || m.Depth != 1 {
		t.Errorf("nearest inheriting ancestor must win; got ok=%v agent=%s depth=%d", ok, m.Agent.Name, m.Depth)
	}
}

// --- 5. Ambiguity fails safe -------------------------------------------------

func TestPickSiteAgent_AmbiguousInheritanceFailsSafe(t *testing.T) {
	f := newScopeFixture()
	a1 := agentAt("group-agent-b", f.group, true)
	a2 := agentAt("group-agent-a", f.group, true)
	m, amb, ok := pickSiteAgent([]db.RelayAgent{a1, a2}, f.parents, f.hotelA, nil)
	if ok {
		t.Fatal("two equally valid inherited agents must NOT be silently resolved")
	}
	if len(amb) != 2 {
		t.Fatalf("both competitors must be reported, got %d", len(amb))
	}
	if m.Agent.Name != "" {
		t.Error("no agent may be selected when routing is ambiguous")
	}
	// Deterministic order so the operator message is stable.
	if amb[0].Name != "group-agent-a" || amb[1].Name != "group-agent-b" {
		t.Errorf("competitors should be name-sorted, got %s then %s", amb[0].Name, amb[1].Name)
	}
	if got := agentNames(amb); got != "group-agent-a, group-agent-b" {
		t.Errorf("agentNames = %q", got)
	}
}

// An exact-level tie keeps the legacy heartbeat tie-break rather than becoming
// ambiguous — otherwise upgrading would break sites that already run two agents.
func TestPickSiteAgent_ExactTieUsesHeartbeatNotAmbiguity(t *testing.T) {
	f := newScopeFixture()
	older := agentAt("older", f.hotelA, false)
	newer := agentAt("newer", f.hotelA, false)
	old := time.Now().UTC().Add(-time.Hour)
	older.LastHeartbeat = &old
	m, amb, ok := pickSiteAgent([]db.RelayAgent{older, newer}, f.parents, f.hotelA, nil)
	if !ok || amb != nil {
		t.Fatalf("exact matches must not become ambiguous on upgrade; ok=%v amb=%v", ok, amb)
	}
	if m.Agent.Name != "newer" {
		t.Errorf("most recent heartbeat wins, got %s", m.Agent.Name)
	}
}

// --- 6. Eligibility + disabled agents ----------------------------------------

func TestPickSiteAgent_DisabledAndIneligibleAreSkipped(t *testing.T) {
	f := newScopeFixture()
	disabled := agentAt("disabled", f.hotelA, false)
	disabled.Enabled = false
	if _, _, ok := pickSiteAgent([]db.RelayAgent{disabled}, f.parents, f.hotelA, nil); ok {
		t.Error("a disabled agent must never be selected")
	}
	// Online-only filter: an offline inherited agent is skipped by the online pass.
	group := agentAt("group-agent", f.group, true)
	onlineOnly := func(a db.RelayAgent) bool { return a.Name != "group-agent" }
	if _, _, ok := pickSiteAgent([]db.RelayAgent{group}, f.parents, f.hotelA, onlineOnly); ok {
		t.Error("an ineligible agent must not be selected")
	}
	// …but the unfiltered pass still finds it, which is how offline agents get
	// queued for and drain later.
	if _, _, ok := pickSiteAgent([]db.RelayAgent{group}, f.parents, f.hotelA, nil); !ok {
		t.Error("the fallback pass must still find the offline inherited agent")
	}
}

// A sibling site must not be served by an agent assigned to the other sibling.
func TestPickSiteAgent_SiblingIsNotCovered(t *testing.T) {
	f := newScopeFixture()
	a := agentAt("chr-agent", f.hotelA, true) // inheritance on, but CAC is a SIBLING
	if _, _, ok := pickSiteAgent([]db.RelayAgent{a}, f.parents, f.hotelB, nil); ok {
		t.Error("inheritance goes DOWN the tree only — a sibling site must not be covered")
	}
}

// A cyclic/corrupt parent map must terminate rather than loop forever.
func TestPickSiteAgent_CyclicTreeTerminates(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	parents := map[uuid.UUID]uuid.UUID{a: b, b: a}
	done := make(chan struct{})
	go func() {
		_, _, _ = pickSiteAgent(nil, parents, a, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pickSiteAgent looped on a cyclic location tree")
	}
}

// --- 7. Effective scope is honest about what is covered ----------------------

func TestDescribeAgentScope_ExactSaysChildrenNotCovered(t *testing.T) {
	f := newScopeFixture()
	sc := describeAgentScope(agentAt("group-agent", f.group, false), f.locs)
	if sc.Mode != "exact" || sc.CoveredCount != 1 {
		t.Errorf("group agent without inheritance is exact-only; got mode=%s count=%d", sc.Mode, sc.CoveredCount)
	}
	if !strings.Contains(sc.Explanation, "NOT covered") {
		t.Errorf("the explanation must state that child sites are not covered; got: %s", sc.Explanation)
	}
	if len(sc.CoveredSites) != 0 {
		t.Errorf("exact scope covers no descendants, got %v", sc.CoveredSites)
	}
}

func TestDescribeAgentScope_InheritedListsDescendants(t *testing.T) {
	f := newScopeFixture()
	sc := describeAgentScope(agentAt("group-agent", f.group, true), f.locs)
	if sc.Mode != "inherited" {
		t.Fatalf("mode = %s, want inherited", sc.Mode)
	}
	// group covers CHR, CAC and CHR-B1 → itself + 3.
	if sc.CoveredCount != 4 {
		t.Errorf("covered count = %d, want 4 (self + 3 descendants)", sc.CoveredCount)
	}
	for _, want := range []string{"CAC", "CHR", "CHR-B1"} {
		found := false
		for _, g := range sc.CoveredSites {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("descendant %q missing from %v", want, sc.CoveredSites)
		}
	}
}

func TestDescribeAgentScope_Unassigned(t *testing.T) {
	sc := describeAgentScope(db.RelayAgent{Name: "orphan", Enabled: true}, nil)
	if sc.Mode != "unassigned" || sc.CoveredCount != 0 {
		t.Errorf("an unassigned agent serves nothing; got mode=%s count=%d", sc.Mode, sc.CoveredCount)
	}
}
