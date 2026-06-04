package api

import (
	"net/http"

	"github.com/google/uuid"
)

// Multi-Site / Hotel View (#22). Attributes each device to its site (the
// nearest hotel/group ancestor in the locations tree) and rolls up device
// count, up/down health, category mix and open alerts per site — so an operator
// sees each hotel's posture at a glance and can drill into its inventory.

// siteKinds are the location kinds that constitute a "site" for rollups.
var siteKinds = map[string]bool{"hotel": true, "group": true}

// resolveSite walks up the parent chain from start to the nearest ancestor
// (including start) whose kind is a site kind. Pure + cycle-guarded so it is
// unit-testable and safe on malformed trees. Returns uuid.Nil if none found.
func resolveSite(start uuid.UUID, parent map[uuid.UUID]uuid.UUID, kind map[uuid.UUID]string) uuid.UUID {
	cur := start
	for i := 0; i < 64; i++ { // depth guard
		if siteKinds[kind[cur]] {
			return cur
		}
		next, ok := parent[cur]
		if !ok || next == uuid.Nil || next == cur {
			return uuid.Nil
		}
		cur = next
	}
	return uuid.Nil
}

type siteRollup struct {
	SiteID     string         `json:"site_id"`
	SiteName   string         `json:"site_name"`
	Kind       string         `json:"kind"`
	Devices    int            `json:"devices"`
	Up         int            `json:"up"`
	Down       int            `json:"down"`
	Warning    int            `json:"warning"`
	Unknown    int            `json:"unknown"`
	OpenAlerts int            `json:"open_alerts"`
	ByCategory map[string]int `json:"by_category"`
}

// sitesOverview handles GET /sites/overview.
func (s *Server) sitesOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locs, err := s.queries.ListLocations(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	parent := make(map[uuid.UUID]uuid.UUID, len(locs))
	kind := make(map[uuid.UUID]string, len(locs))
	name := make(map[uuid.UUID]string, len(locs))
	for _, l := range locs {
		kind[l.ID] = l.Kind
		name[l.ID] = l.Name
		if l.ParentID != nil {
			parent[l.ID] = *l.ParentID
		}
	}

	devices, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	alertRows, err := s.queries.OpenAlertCountsByDevice(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	alertByDevice := make(map[uuid.UUID]int, len(alertRows))
	for _, a := range alertRows {
		alertByDevice[a.DeviceID] = int(a.N)
	}

	const unassigned = "unassigned"
	rollups := map[string]*siteRollup{}
	get := func(id, nm, knd string) *siteRollup {
		r := rollups[id]
		if r == nil {
			r = &siteRollup{SiteID: id, SiteName: nm, Kind: knd, ByCategory: map[string]int{}}
			rollups[id] = r
		}
		return r
	}

	for _, d := range devices {
		var sr *siteRollup
		if d.LocationID == nil {
			sr = get(unassigned, "Unassigned", "")
		} else if site := resolveSite(*d.LocationID, parent, kind); site != uuid.Nil {
			sr = get(site.String(), name[site], kind[site])
		} else {
			sr = get(unassigned, "Unassigned", "")
		}
		sr.Devices++
		sr.ByCategory[d.Category]++
		switch d.Status {
		case "up":
			sr.Up++
		case "down":
			sr.Down++
		case "warning", "needs_attention":
			sr.Warning++
		default:
			sr.Unknown++
		}
		sr.OpenAlerts += alertByDevice[d.ID]
	}

	// Stable output: sites first (by device count desc), unassigned last.
	out := make([]*siteRollup, 0, len(rollups))
	for id, r := range rollups {
		if id != unassigned {
			out = append(out, r)
		}
	}
	sortSitesByDevices(out)
	if u, ok := rollups[unassigned]; ok {
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}

func sortSitesByDevices(s []*siteRollup) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Devices > s[j-1].Devices; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
