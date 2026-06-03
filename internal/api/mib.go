package api

import (
	"encoding/json"
	"net/http"

	"github.com/coralsearesorts/hims/internal/mibparse"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

type uploadMibReq struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// uploadMib parses an uploaded MIB into the OID library. Parsing is in-memory;
// the file's objects are persisted, unresolved nodes included (flagged) so the
// operator sees what couldn't be reduced.
func (s *Server) uploadMib(w http.ResponseWriter, r *http.Request) {
	var req uploadMibReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Content == "" {
		http.Error(w, "name and content are required", http.StatusBadRequest)
		return
	}
	objs, err := mibparse.Parse(req.Content)
	if err != nil {
		writeErr(w, errBadRequest(err.Error()))
		return
	}
	unresolved := 0
	for _, o := range objs {
		if o.Unresolved {
			unresolved++
		}
	}
	file, err := s.queries.CreateMibFile(r.Context(), db.CreateMibFileParams{
		Name: req.Name, ObjectCount: int32(len(objs)), Unresolved: int32(unresolved),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, o := range objs {
		_ = s.queries.InsertMibObject(r.Context(), db.InsertMibObjectParams{
			MibFileID: file.ID, Name: o.Name, Oid: o.OID,
			Syntax: nonEmptyStr(o.Syntax), Kind: o.Kind, Unresolved: o.Unresolved,
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"file": file, "parsed": len(objs), "unresolved": unresolved,
	})
}

func (s *Server) listMibFiles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListMibFiles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) listMibObjects(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.queries.ListMibObjects(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) listOIDMappings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListOIDMappings(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type oidMappingReq struct {
	OID       string  `json:"oid"`
	Label     string  `json:"label"`
	MetricKey *string `json:"metric_key"`
	Vendor    *string `json:"vendor"`
	Template  *string `json:"template"`
	Notes     *string `json:"notes"`
}

func (s *Server) createOIDMapping(w http.ResponseWriter, r *http.Request) {
	var req oidMappingReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.OID == "" || req.Label == "" {
		http.Error(w, "oid and label are required", http.StatusBadRequest)
		return
	}
	m, err := s.queries.CreateOIDMapping(r.Context(), db.CreateOIDMappingParams{
		Oid: req.OID, Label: req.Label, MetricKey: req.MetricKey,
		Vendor: req.Vendor, Template: req.Template, Notes: req.Notes,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) deleteOIDMapping(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteOIDMapping(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nonEmptyStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
