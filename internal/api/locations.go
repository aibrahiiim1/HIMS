package api

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// locationKinds is the location-tree taxonomy (mirrors the locations.kind
// CHECK). Order is the natural nesting depth, used by the UI for child-kind
// suggestions.
var locationKinds = []string{"group", "hotel", "building", "floor", "area", "room", "rack", "office"}

func validLocationKind(k string) bool {
	for _, v := range locationKinds {
		if v == k {
			return true
		}
	}
	return false
}

// listAllLocations returns the full flat location set (the UI builds the tree).
func (s *Server) listAllLocations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListLocations(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type createLocationReq struct {
	ParentID *string `json:"parent_id"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Code     string  `json:"code"`
}

// createLocation adds a node to the tree (parent_id null = a root group).
func (s *Server) createLocation(w http.ResponseWriter, r *http.Request) {
	var req createLocationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if !validLocationKind(req.Kind) {
		http.Error(w, "invalid kind "+strconv.Quote(req.Kind)+"; use one of: "+strings.Join(locationKinds, ", "), http.StatusBadRequest)
		return
	}
	loc, err := s.queries.CreateLocation(r.Context(), db.CreateLocationParams{
		ParentID: parseUUIDPtr(req.ParentID), Kind: req.Kind,
		Name: strings.TrimSpace(req.Name), Code: strPtr(req.Code), Metadata: []byte("{}"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, loc)
}

type updateLocationReq struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

// updateLocation renames a node / sets its code.
func (s *Server) updateLocation(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req updateLocationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	loc, err := s.queries.UpdateLocation(ctx, db.UpdateLocationParams{ID: id, Name: strings.TrimSpace(req.Name), Code: strPtr(req.Code)})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, loc)
}

// listAllSubnets returns every subnet (the Locations page groups by location).
func (s *Server) listAllSubnets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListSubnets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type createSubnetReq struct {
	CIDR string `json:"cidr"`
	Name string `json:"name"`
	VLAN *int32 `json:"vlan_id"`
}

// createSubnet attaches a CIDR to a location (site). The By-Site scan expands
// these, and the credential resolver matches device IPs against them.
func (s *Server) createSubnet(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req createSubnetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(req.CIDR))
	if err != nil {
		http.Error(w, "invalid cidr (e.g. 172.21.96.0/24): "+err.Error(), http.StatusBadRequest)
		return
	}
	sn, err := s.queries.CreateSubnet(ctx, db.CreateSubnetParams{
		LocationID: id, Cidr: prefix.Masked(), Name: strPtr(req.Name), VlanID: req.VLAN,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sn)
}

// deleteSubnet removes a CIDR from a site.
func (s *Server) deleteSubnet(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteSubnet(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteLocation removes a node and its whole subtree (FK ON DELETE CASCADE);
// devices anchored to deleted nodes have their location_id set NULL.
func (s *Server) deleteLocation(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteLocation(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
