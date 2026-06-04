package api

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"sort"
	"strconv"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Data Quality quick-action: reconcile unassigned devices to a site by matching
// their primary IP against the site subnets the operator already defined
// (Locations → Subnets). This is evidence-based — a device is only assigned when
// its IP falls inside a declared CIDR. Devices whose IP matches no declared
// subnet stay unassigned (we never guess a site). Reversible via bulk-assign.

// locationKindRank ranks a location's specificity. When more than one declared
// subnet contains a device's IP (e.g. a hotel and the umbrella group both
// declare 172.21.96.0/24), the more specific location wins.
var locationKindRank = map[string]int{
	"group": 0, "region": 0,
	"hotel": 1, "campus": 1, "site": 1,
	"building": 2,
	"floor":    3, "area": 3,
	"room": 4, "office": 4,
	"rack": 5,
}

type subnetCandidate struct {
	Prefix netip.Prefix
	LocID  uuid.UUID
	Rank   int
}

// matchSubnet returns the best location for ip: among the declared subnets that
// contain ip, the one with the longest prefix wins; ties break toward the more
// specific location kind. ok=false when no declared subnet contains ip. Pure →
// unit-tested.
func matchSubnet(ip netip.Addr, cands []subnetCandidate) (uuid.UUID, bool) {
	ip = ip.Unmap()
	var best uuid.UUID
	bestBits, bestRank := -1, -1
	found := false
	for _, c := range cands {
		if !c.Prefix.Contains(ip) {
			continue
		}
		b := c.Prefix.Bits()
		if b > bestBits || (b == bestBits && c.Rank > bestRank) {
			best, bestBits, bestRank, found = c.LocID, b, c.Rank, true
		}
	}
	return best, found
}

type reconcileSitesReq struct {
	DryRun bool `json:"dry_run"`
}

type reconcileAssignment struct {
	DeviceID     string `json:"device_id"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	LocationID   string `json:"location_id"`
	LocationName string `json:"location_name"`
}

type reconcileSiteCount struct {
	LocationID   string `json:"location_id"`
	LocationName string `json:"location_name"`
	Count        int    `json:"count"`
}

// reconcileSites assigns unassigned devices to a site by IP↔subnet match.
// Defaults to a dry run (preview); apply requires {"dry_run": false}.
func (s *Server) reconcileSites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	req := reconcileSitesReq{DryRun: true}
	if r.Body != nil {
		// Lenient: empty body keeps the safe dry-run default.
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	subnets, err := s.queries.ListSubnets(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	locs, err := s.queries.ListLocations(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}

	locName := make(map[uuid.UUID]string, len(locs))
	locRank := make(map[uuid.UUID]int, len(locs))
	for _, l := range locs {
		locName[l.ID] = l.Name
		locRank[l.ID] = locationKindRank[l.Kind]
	}

	cands := make([]subnetCandidate, 0, len(subnets))
	for _, sn := range subnets {
		if !sn.Cidr.IsValid() {
			continue
		}
		cands = append(cands, subnetCandidate{Prefix: sn.Cidr, LocID: sn.LocationID, Rank: locRank[sn.LocationID]})
	}

	var assignments []reconcileAssignment
	byLoc := map[uuid.UUID][]uuid.UUID{}
	unmatched := 0
	for _, d := range devs {
		if d.LocationID != nil { // only touch unassigned devices
			continue
		}
		if d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
			unmatched++
			continue
		}
		loc, ok := matchSubnet(*d.PrimaryIp, cands)
		if !ok {
			unmatched++
			continue
		}
		byLoc[loc] = append(byLoc[loc], d.ID)
		assignments = append(assignments, reconcileAssignment{
			DeviceID: d.ID.String(), Name: d.Name, IP: d.PrimaryIp.String(),
			LocationID: loc.String(), LocationName: locName[loc],
		})
	}

	// Stable per-site rollup.
	bySite := make([]reconcileSiteCount, 0, len(byLoc))
	for loc, ids := range byLoc {
		bySite = append(bySite, reconcileSiteCount{LocationID: loc.String(), LocationName: locName[loc], Count: len(ids)})
	}
	sort.Slice(bySite, func(i, j int) bool {
		if bySite[i].Count != bySite[j].Count {
			return bySite[i].Count > bySite[j].Count
		}
		return bySite[i].LocationName < bySite[j].LocationName
	})

	matched := len(assignments)

	if req.DryRun {
		sample := assignments
		const cap = 200
		if len(sample) > cap {
			sample = sample[:cap]
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dry_run":     true,
			"matched":     matched,
			"unmatched":   unmatched,
			"by_site":     bySite,
			"assignments": sample,
		})
		return
	}

	// Apply: one bulk update per target site, audited.
	actor := s.actor(r)
	updated := int64(0)
	for loc, ids := range byLoc {
		loc := loc
		n, err := s.queries.BulkAssignClassification(ctx, db.BulkAssignClassificationParams{
			Ids: ids, LocationID: &loc,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		updated += n
		s.auditAs(actor, r, "data_quality", "devices.reconcile_site", "location", loc.String(),
			"Assigned "+locName[loc]+" to "+strconv.FormatInt(n, 10)+" device(s) by subnet match",
			map[string]any{"location": locName[loc], "count": n})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run":   false,
		"updated":   updated,
		"unmatched": unmatched,
		"by_site":   bySite,
	})
}
