package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Lookups are operator-managed value lists for the Inventory classification
// dropdowns: kind "class" (device class) and "vlan".

func (s *Server) listLookups(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "class" && kind != "vlan" {
		http.Error(w, "kind must be 'class' or 'vlan'", http.StatusBadRequest)
		return
	}
	rows, err := s.queries.ListLookups(r.Context(), kind)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type createLookupReq struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func (s *Server) createLookup(w http.ResponseWriter, r *http.Request) {
	var req createLookupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Kind != "class" && req.Kind != "vlan" {
		http.Error(w, "kind must be 'class' or 'vlan'", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		http.Error(w, "value is required", http.StatusBadRequest)
		return
	}
	row, err := s.queries.CreateLookup(r.Context(), db.CreateLookupParams{Kind: req.Kind, Value: strings.TrimSpace(req.Value)})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) deleteLookup(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteLookup(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
