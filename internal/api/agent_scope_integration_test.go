package api

import (
	"context"
	"os"
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-backed checks for the opt-in agent scope. The pure routing rules are
// covered in agent_scope_test.go; this exercises the parts only a real database
// can prove: the migration applied, the default is false for rows that existed
// before it, the setter round-trips, and the resolver works off real rows.
//
// Runs only when HIMS_TEST_DATABASE_URL points at a MIGRATED throwaway database.
//
//	HIMS_TEST_DATABASE_URL=postgres://... go test ./internal/api -run Integration
func TestIntegration_AgentScopePersistence(t *testing.T) {
	dsn := os.Getenv("HIMS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set HIMS_TEST_DATABASE_URL to a migrated throwaway database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	// --- location tree: group -> hotel
	group, err := q.CreateLocation(ctx, db.CreateLocationParams{Name: "IT-Group-" + uuid.NewString()[:8], Kind: "group", Metadata: []byte("{}")})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	hotel, err := q.CreateLocation(ctx, db.CreateLocationParams{Name: "IT-Hotel-" + uuid.NewString()[:8], Kind: "hotel", ParentID: &group.ID, Metadata: []byte("{}")})
	if err != nil {
		t.Fatalf("create hotel: %v", err)
	}
	t.Cleanup(func() {
		_ = q.DeleteLocation(ctx, hotel.ID)
		_ = q.DeleteLocation(ctx, group.ID)
	})

	// --- an agent created the ordinary way must DEFAULT to exact-only. This is
	// the backward-compatibility guarantee: every agent that existed before the
	// migration keeps its behaviour.
	ag, err := q.CreateRelayAgent(ctx, db.CreateRelayAgentParams{
		Name: "it-agent-" + uuid.NewString()[:8], LocationID: &group.ID, TokenHash: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = q.DeleteRelayAgent(ctx, ag.ID) })

	if ag.IncludeDescendants {
		t.Fatal("a newly created agent must default to include_descendants=false (exact-only)")
	}

	parents := map[uuid.UUID]uuid.UUID{hotel.ID: group.ID}
	agentsOf := func() []db.RelayAgent {
		all, lerr := q.ListRelayAgents(ctx)
		if lerr != nil {
			t.Fatalf("list agents: %v", lerr)
		}
		var mine []db.RelayAgent
		for _, a := range all {
			if a.ID == ag.ID {
				mine = append(mine, a)
			}
		}
		return mine
	}

	// The group agent must NOT serve the child hotel while the flag is off.
	if _, _, ok := pickSiteAgent(agentsOf(), parents, hotel.ID, nil); ok {
		t.Error("with include_descendants=false a group agent must not serve the child hotel")
	}

	// --- enable inheritance and re-resolve off the persisted row.
	if err := q.SetRelayAgentIncludeDescendants(ctx, db.SetRelayAgentIncludeDescendantsParams{
		ID: ag.ID, IncludeDescendants: true,
	}); err != nil {
		t.Fatalf("set include_descendants: %v", err)
	}
	reloaded, err := q.GetRelayAgent(ctx, ag.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.IncludeDescendants {
		t.Fatal("include_descendants did not round-trip through the database")
	}
	m, amb, ok := pickSiteAgent(agentsOf(), parents, hotel.ID, nil)
	if !ok || amb != nil {
		t.Fatalf("after enabling inheritance the child hotel must resolve; ok=%v amb=%v", ok, amb)
	}
	if m.Kind != agentMatchInherited || m.Via != group.ID {
		t.Errorf("want inherited via the group, got kind=%s via=%v", m.Kind, m.Via)
	}

	// --- effective scope, computed from real locations.
	locs, err := q.ListLocations(ctx)
	if err != nil {
		t.Fatalf("list locations: %v", err)
	}
	sc := describeAgentScope(reloaded, locs)
	if sc.Mode != "inherited" {
		t.Errorf("effective scope mode = %s, want inherited", sc.Mode)
	}
	found := false
	for _, n := range sc.CoveredSites {
		if n == hotel.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("the child hotel %q should be listed as covered, got %v", hotel.Name, sc.CoveredSites)
	}

	// --- turning it back off must restore exact-only behaviour.
	if err := q.SetRelayAgentIncludeDescendants(ctx, db.SetRelayAgentIncludeDescendantsParams{
		ID: ag.ID, IncludeDescendants: false,
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, _, ok := pickSiteAgent(agentsOf(), parents, hotel.ID, nil); ok {
		t.Error("disabling inheritance must immediately restore exact-only routing")
	}
}
