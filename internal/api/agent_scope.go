package api

import (
	"context"
	"sort"
	"strings"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Relay-agent site scope resolution.
//
// Default is EXACT: an agent serves only the site it is assigned to. That is the
// historical behaviour and stays untouched, because widening it silently would
// hand every existing agent jobs for sites it was never meant to cover.
//
// An agent may OPT IN to `include_descendants`, after which it may also serve
// devices in sites beneath its assigned location. The rules, in order:
//
//  1. An agent assigned DIRECTLY to the device's site always wins. A child-site
//     agent therefore takes precedence over an inherited parent agent.
//  2. Otherwise walk up the location tree. The NEAREST ancestor that has any
//     opted-in agent wins; farther ancestors are not consulted.
//  3. If that nearest ancestor level offers more than one equally valid agent,
//     routing FAILS with an ambiguity reason. It never picks one silently —
//     an arbitrary choice would send jobs to a collector the operator did not
//     intend and be near-impossible to diagnose later.
//
// Exact matches keep their pre-existing tie-break (most recent heartbeat) rather
// than becoming ambiguous, so upgrading changes nothing for agents already in
// service.

// agentMatchKind describes HOW an agent was selected, for diagnostics.
const (
	agentMatchExact     = "exact"     // assigned directly to the device's site
	agentMatchInherited = "inherited" // opted in, assigned to an ancestor site
)

// agentScopeMatch is a resolved agent plus why it was chosen.
type agentScopeMatch struct {
	Agent db.RelayAgent
	Kind  string    // agentMatchExact | agentMatchInherited
	Via   uuid.UUID // location the agent is assigned to (== device site when exact)
	Depth int       // 0 = exact, 1 = parent, 2 = grandparent, …
}

// pickSiteAgent resolves the agent for loc from the full agent list and a
// child→parent map. Pure: no DB, no clock — every rule below is unit-tested.
//
// Returns (match, nil, true) on a clean pick, (zero, competing, false) when the
// nearest inheriting level is ambiguous, and (zero, nil, false) when nothing
// covers the site. `eligible` filters candidates (e.g. online-only); pass nil to
// accept every enabled agent.
func pickSiteAgent(
	agents []db.RelayAgent,
	parents map[uuid.UUID]uuid.UUID,
	loc uuid.UUID,
	eligible func(db.RelayAgent) bool,
) (agentScopeMatch, []db.RelayAgent, bool) {
	usable := func(a db.RelayAgent) bool {
		if !a.Enabled || a.LocationID == nil {
			return false
		}
		return eligible == nil || eligible(a)
	}

	// --- 1. Exact: assigned directly to this site. Unchanged legacy behaviour,
	// including the most-recent-heartbeat tie-break.
	var exact []db.RelayAgent
	for _, a := range agents {
		if usable(a) && *a.LocationID == loc {
			exact = append(exact, a)
		}
	}
	if len(exact) > 0 {
		best := exact[0]
		for _, a := range exact[1:] {
			if hbAfter(a.LastHeartbeat, best.LastHeartbeat) {
				best = a
			}
		}
		return agentScopeMatch{Agent: best, Kind: agentMatchExact, Via: loc, Depth: 0}, nil, true
	}

	// --- 2. Inherited: nearest ancestor with opted-in agents. Guard against a
	// cyclic/corrupt tree with a visited set.
	visited := map[uuid.UUID]bool{loc: true}
	cur, depth := loc, 0
	for {
		parent, ok := parents[cur]
		if !ok || visited[parent] {
			break
		}
		visited[parent] = true
		depth++
		var inherited []db.RelayAgent
		for _, a := range agents {
			if usable(a) && a.IncludeDescendants && *a.LocationID == parent {
				inherited = append(inherited, a)
			}
		}
		switch {
		case len(inherited) == 1:
			return agentScopeMatch{Agent: inherited[0], Kind: agentMatchInherited, Via: parent, Depth: depth}, nil, true
		case len(inherited) > 1:
			// --- 3. Ambiguous at the nearest inheriting level: fail safe.
			sort.Slice(inherited, func(i, j int) bool { return inherited[i].Name < inherited[j].Name })
			return agentScopeMatch{}, inherited, false
		}
		cur = parent
	}
	return agentScopeMatch{}, nil, false
}

// agentNames renders competing agents for an operator-facing ambiguity message.
func agentNames(list []db.RelayAgent) string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

// resolveSiteAgentScoped is the DB-backed wrapper around pickSiteAgent.
func (s *Server) resolveSiteAgentScoped(ctx context.Context, loc uuid.UUID, eligible func(db.RelayAgent) bool) (agentScopeMatch, []db.RelayAgent, bool) {
	agents, err := s.queries.ListRelayAgents(ctx)
	if err != nil {
		return agentScopeMatch{}, nil, false
	}
	return pickSiteAgent(agents, s.locationParents(ctx), loc, eligible)
}

// locationsByID loads the location tree (small table, per-request fetch is fine).
func (s *Server) locationsByID(ctx context.Context) []db.Location {
	locs, err := s.queries.ListLocations(ctx)
	if err != nil {
		return nil
	}
	return locs
}

// locationNameOf resolves a location's display name, "" when unknown.
func locationNameOf(locs []db.Location, id uuid.UUID) string {
	for _, l := range locs {
		if l.ID == id {
			return l.Name
		}
	}
	return ""
}

// agentEffectiveScope describes an agent's reach for the Agents UI and
// diagnostics: the site it is assigned to, whether inheritance is on, and — when
// it is — the descendant sites it actually covers.
//
// A group-level agent WITHOUT inheritance reports "exact only", so nobody reads
// a group assignment as covering the hotels beneath it.
type agentEffectiveScope struct {
	Mode         string   `json:"mode"` // "exact" | "inherited" | "unassigned"
	SiteID       string   `json:"site_id,omitempty"`
	SiteName     string   `json:"site_name,omitempty"`
	CoveredSites []string `json:"covered_sites,omitempty"` // descendant site names (inheritance on)
	CoveredCount int      `json:"covered_count"`
	Explanation  string   `json:"explanation"`
}

// describeAgentScope builds the effective scope for one agent.
func describeAgentScope(a db.RelayAgent, locs []db.Location) agentEffectiveScope {
	if a.LocationID == nil {
		return agentEffectiveScope{
			Mode: "unassigned", CoveredCount: 0,
			Explanation: "Not assigned to a site — this agent serves no devices. Assign it to the site whose devices it should collect.",
		}
	}
	name := ""
	for _, l := range locs {
		if l.ID == *a.LocationID {
			name = l.Name
		}
	}
	if !a.IncludeDescendants {
		return agentEffectiveScope{
			Mode: "exact", SiteID: a.LocationID.String(), SiteName: name, CoveredCount: 1,
			Explanation: "Serves ONLY devices assigned to " + nameOr(name, "its site") +
				". Sites beneath it are NOT covered — enable “Include descendant sites” if this agent should also collect them.",
		}
	}
	// Inheritance on: enumerate descendants.
	parents := map[uuid.UUID]uuid.UUID{}
	for _, l := range locs {
		if l.ParentID != nil {
			parents[l.ID] = *l.ParentID
		}
	}
	var covered []string
	for _, l := range locs {
		if l.ID == *a.LocationID {
			continue
		}
		// Walk up from l; if we reach the agent's site, l is a descendant.
		seen := map[uuid.UUID]bool{l.ID: true}
		for cur := l.ID; ; {
			p, ok := parents[cur]
			if !ok || seen[p] {
				break
			}
			seen[p] = true
			if p == *a.LocationID {
				covered = append(covered, l.Name)
				break
			}
			cur = p
		}
	}
	sort.Strings(covered)
	exp := "Serves devices assigned to " + nameOr(name, "its site") + " and to every site beneath it"
	if len(covered) > 0 {
		exp += " (" + strings.Join(covered, ", ") + ")"
	} else {
		exp += " — there are no sites beneath it today"
	}
	exp += ". An agent assigned directly to a child site still takes precedence there."
	return agentEffectiveScope{
		Mode: "inherited", SiteID: a.LocationID.String(), SiteName: name,
		CoveredSites: covered, CoveredCount: 1 + len(covered), Explanation: exp,
	}
}

func nameOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
