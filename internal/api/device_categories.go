package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/go-chi/chi/v5"
)

// Operator-managed device categories. Built-in categories are protected: they can be
// relabelled / re-icon'd / hidden, but never deleted (they are produced by the
// classifier, routed by the detail pages, and used by existing devices). Operators may
// add custom categories for MANUAL classification (fully CRUD-able). Category validity
// (for device edits) is the built-in set ∪ every row in device_categories — see
// isValidCategory, which replaces the dropped devices_category_check CHECK constraint.

var categoryValueRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

// isValidCategory reports whether a category value may be written to a device. Built-in
// values are always valid (fast path, no DB); custom values are valid when a row exists.
func (s *Server) isValidCategory(ctx context.Context, c string) bool {
	if validCategory(c) {
		return true
	}
	ok, err := s.queries.IsCategoryValid(ctx, c)
	return err == nil && ok
}

// listDeviceCategories is GET /device-categories — the catalog for the Edit picker and
// the Settings management page, with a live device count per category.
func (s *Server) listDeviceCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListDeviceCategories(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if rows == nil {
		rows = []db.ListDeviceCategoriesRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

type categoryReq struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	Icon      string `json:"icon"`
	Enabled   *bool  `json:"enabled"`
	SortOrder *int32 `json:"sort_order"`
}

// createDeviceCategory is POST /device-categories — add a CUSTOM category (manual-only).
func (s *Server) createDeviceCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	value := strings.ToLower(strings.TrimSpace(req.Value))
	if !categoryValueRe.MatchString(value) {
		http.Error(w, "value must be a slug: lowercase letter, then letters/digits/underscore (2-40 chars)", http.StatusBadRequest)
		return
	}
	// Never let a custom row shadow a built-in category value.
	if validCategory(value) {
		http.Error(w, "'"+value+"' is a built-in category — pick a different value", http.StatusConflict)
		return
	}
	if _, err := s.queries.GetDeviceCategory(r.Context(), value); err == nil {
		http.Error(w, "a category with value '"+value+"' already exists", http.StatusConflict)
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = value
	}
	sort := int32(200)
	if req.SortOrder != nil {
		sort = *req.SortOrder
	}
	row, err := s.queries.CreateDeviceCategory(r.Context(), db.CreateDeviceCategoryParams{
		Value: value, Label: label, Icon: strings.TrimSpace(req.Icon), SortOrder: sort,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "device_category.create", "device_category", value, "Added custom device category "+value, map[string]any{"label": label})
	writeJSON(w, http.StatusCreated, row)
}

// updateDeviceCategory is PATCH /device-categories/{value} — relabel / re-icon / hide-show
// / reorder ANY category (built-in or custom). value + builtin are immutable.
func (s *Server) updateDeviceCategory(w http.ResponseWriter, r *http.Request) {
	value := chi.URLParam(r, "value")
	existing, err := s.queries.GetDeviceCategory(r.Context(), value)
	if err != nil {
		http.Error(w, "category not found", http.StatusNotFound)
		return
	}
	var req categoryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	label := existing.Label
	if strings.TrimSpace(req.Label) != "" {
		label = strings.TrimSpace(req.Label)
	}
	icon := existing.Icon
	if req.Icon != "" {
		icon = strings.TrimSpace(req.Icon)
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	sort := existing.SortOrder
	if req.SortOrder != nil {
		sort = *req.SortOrder
	}
	row, err := s.queries.UpdateDeviceCategory(r.Context(), db.UpdateDeviceCategoryParams{
		Value: value, Label: label, Icon: icon, Enabled: enabled, SortOrder: sort,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "device_category.update", "device_category", value, "Updated device category "+value, map[string]any{"enabled": enabled})
	writeJSON(w, http.StatusOK, row)
}

// deleteDeviceCategory is DELETE /device-categories/{value} — CUSTOM categories only.
// Built-ins are protected (blocked with an honest reason). Devices still using a deleted
// custom category are reassigned to 'unknown' so none are orphaned.
func (s *Server) deleteDeviceCategory(w http.ResponseWriter, r *http.Request) {
	value := chi.URLParam(r, "value")
	existing, err := s.queries.GetDeviceCategory(r.Context(), value)
	if err != nil {
		http.Error(w, "category not found", http.StatusNotFound)
		return
	}
	if existing.Builtin {
		http.Error(w, "'"+value+"' is a built-in category and cannot be deleted (it is produced by auto-classification and used by device detail pages). Hide it instead.", http.StatusForbidden)
		return
	}
	n, _ := s.queries.CountDevicesInCategory(r.Context(), value)
	if n > 0 {
		if err := s.queries.ReassignDeviceCategory(r.Context(), db.ReassignDeviceCategoryParams{Category: value, Category_2: "unknown"}); err != nil {
			writeErr(w, err)
			return
		}
	}
	if err := s.queries.DeleteDeviceCategory(r.Context(), value); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "device_category.delete", "device_category", value, "Deleted custom device category "+value, map[string]any{"reassigned_to_unknown": n})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reassigned": n})
}
