package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// ---- helpers ----------------------------------------------------------------

func parseUUIDParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		http.Error(w, "invalid "+name, http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// audit records an operator/system action. It never fails the request — an
// audit write error is logged, not surfaced. actor comes from the X-Actor
// header when present (set by a future auth layer), else "operator".
func (s *Server) audit(r *http.Request, category, action, entityType, entityID, summary string, details map[string]any) {
	actor := r.Header.Get("X-Actor")
	if actor == "" {
		actor = "operator"
	}
	raw := []byte("{}")
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			raw = b
		}
	}
	if err := s.queries.InsertAuditLog(r.Context(), db.InsertAuditLogParams{
		Actor: actor, Action: action, Category: category,
		EntityType: entityType, EntityID: entityID, Summary: summary, Details: raw,
	}); err != nil {
		slog.Warn("audit write failed", "action", action, "error", err)
	}
}

func jsonOrEmpty(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

// ===== RBAC: users / roles / permissions ====================================

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListUsers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string  `json:"username"`
		FullName   string  `json:"full_name"`
		Email      string  `json:"email"`
		IsActive   *bool   `json:"is_active"`
		LocationID *string `json:"location_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	row, err := s.queries.CreateUser(r.Context(), db.CreateUserParams{
		Username: req.Username, FullName: req.FullName, Email: req.Email, IsActive: active,
		LocationID: parseUUIDPtr(req.LocationID),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "user", "user.create", "user", row.ID.String(), "Created user "+row.Username, nil)
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		FullName   string  `json:"full_name"`
		Email      string  `json:"email"`
		IsActive   bool    `json:"is_active"`
		LocationID *string `json:"location_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	row, err := s.queries.UpdateUser(r.Context(), db.UpdateUserParams{
		ID: id, FullName: req.FullName, Email: req.Email, IsActive: req.IsActive,
		LocationID: parseUUIDPtr(req.LocationID),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "user", "user.update", "user", id.String(), "Updated user "+row.Username, nil)
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteUser(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "user", "user.delete", "user", id.String(), "Deleted user", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userRoles(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.queries.RolesForUser(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) setUserRoles(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		RoleIDs []uuid.UUID `json:"role_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	if err := s.queries.SetUserRolesClear(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	for _, rid := range req.RoleIDs {
		if err := s.queries.AddUserRole(ctx, db.AddUserRoleParams{UserID: id, RoleID: rid}); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.audit(r, "user", "user.roles.set", "user", id.String(), "Updated role assignments", map[string]any{"role_count": len(req.RoleIDs)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListRoles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	row, err := s.queries.CreateRole(r.Context(), db.CreateRoleParams{Name: req.Name, Description: req.Description})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "user", "role.create", "role", row.ID.String(), "Created role "+row.Name, nil)
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteRole(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "user", "role.delete", "role", id.String(), "Deleted role", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rolePermissions(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.queries.PermissionsForRole(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) setRolePermissions(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		PermissionIDs []uuid.UUID `json:"permission_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	if err := s.queries.SetRolePermissionsClear(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	for _, pid := range req.PermissionIDs {
		if err := s.queries.AddRolePermission(ctx, db.AddRolePermissionParams{RoleID: id, PermissionID: pid}); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.audit(r, "user", "role.permissions.set", "role", id.String(), "Updated role permissions", map[string]any{"permission_count": len(req.PermissionIDs)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPermissions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListPermissions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) createPermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code        string `json:"code"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Code == "" {
		http.Error(w, "code is required", http.StatusBadRequest)
		return
	}
	row, err := s.queries.CreatePermission(r.Context(), db.CreatePermissionParams{Code: req.Code, Description: req.Description})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "user", "permission.create", "permission", row.ID.String(), "Created permission "+row.Code, nil)
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) deletePermission(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeletePermission(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "user", "permission.delete", "permission", id.String(), "Deleted permission", nil)
	w.WriteHeader(http.StatusNoContent)
}

// ===== Device templates =====================================================

type templateDTO struct {
	ID                  uuid.UUID       `json:"id"`
	Name                string          `json:"name"`
	Vendor              string          `json:"vendor"`
	DeviceType          string          `json:"device_type"`
	DiscoveryRules      json.RawMessage `json:"discovery_rules"`
	MonitoringRules     json.RawMessage `json:"monitoring_rules"`
	ClassificationRules json.RawMessage `json:"classification_rules"`
	Enabled             bool            `json:"enabled"`
}

func toTemplateDTO(t db.DeviceTemplate) templateDTO {
	return templateDTO{
		ID: t.ID, Name: t.Name, Vendor: t.Vendor, DeviceType: t.DeviceType,
		DiscoveryRules: jsonOrEmpty(t.DiscoveryRules), MonitoringRules: jsonOrEmpty(t.MonitoringRules),
		ClassificationRules: jsonOrEmpty(t.ClassificationRules), Enabled: t.Enabled,
	}
}

type templateReq struct {
	Name                string          `json:"name"`
	Vendor              string          `json:"vendor"`
	DeviceType          string          `json:"device_type"`
	DiscoveryRules      json.RawMessage `json:"discovery_rules"`
	MonitoringRules     json.RawMessage `json:"monitoring_rules"`
	ClassificationRules json.RawMessage `json:"classification_rules"`
	Enabled             *bool           `json:"enabled"`
}

func ruleBytes(m json.RawMessage) []byte {
	if len(m) == 0 {
		return []byte("{}")
	}
	return m
}

func (s *Server) listDeviceTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListDeviceTemplates(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]templateDTO, 0, len(rows))
	for _, t := range rows {
		out = append(out, toTemplateDTO(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createDeviceTemplate(w http.ResponseWriter, r *http.Request) {
	var req templateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.queries.CreateDeviceTemplate(r.Context(), db.CreateDeviceTemplateParams{
		Name: req.Name, Vendor: req.Vendor, DeviceType: req.DeviceType,
		DiscoveryRules: ruleBytes(req.DiscoveryRules), MonitoringRules: ruleBytes(req.MonitoringRules),
		ClassificationRules: ruleBytes(req.ClassificationRules), Enabled: enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "template.create", "device_template", row.ID.String(), "Created device template "+row.Name, nil)
	writeJSON(w, http.StatusCreated, toTemplateDTO(row))
}

func (s *Server) updateDeviceTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req templateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.queries.UpdateDeviceTemplate(r.Context(), db.UpdateDeviceTemplateParams{
		ID: id, Name: req.Name, Vendor: req.Vendor, DeviceType: req.DeviceType,
		DiscoveryRules: ruleBytes(req.DiscoveryRules), MonitoringRules: ruleBytes(req.MonitoringRules),
		ClassificationRules: ruleBytes(req.ClassificationRules), Enabled: enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "template.update", "device_template", id.String(), "Updated device template "+row.Name, nil)
	writeJSON(w, http.StatusOK, toTemplateDTO(row))
}

func (s *Server) deleteDeviceTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteDeviceTemplate(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "template.delete", "device_template", id.String(), "Deleted device template", nil)
	w.WriteHeader(http.StatusNoContent)
}

// ===== Vendor fingerprints ==================================================

var fpKinds = map[string]bool{"oid": true, "service": true, "port": true, "http": true, "ssh": true}

func (s *Server) listVendorFingerprints(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListVendorFingerprints(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type fingerprintReq struct {
	Kind       string `json:"kind"`
	Pattern    string `json:"pattern"`
	Vendor     string `json:"vendor"`
	DeviceType string `json:"device_type"`
	Confidence *int32 `json:"confidence"`
	Enabled    *bool  `json:"enabled"`
}

func (s *Server) createVendorFingerprint(w http.ResponseWriter, r *http.Request) {
	var req fingerprintReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !fpKinds[req.Kind] {
		http.Error(w, "kind must be one of oid/service/port/http/ssh", http.StatusBadRequest)
		return
	}
	if req.Pattern == "" {
		http.Error(w, "pattern is required", http.StatusBadRequest)
		return
	}
	conf := int32(50)
	if req.Confidence != nil {
		conf = *req.Confidence
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.queries.CreateVendorFingerprint(r.Context(), db.CreateVendorFingerprintParams{
		Kind: req.Kind, Pattern: req.Pattern, Vendor: req.Vendor, DeviceType: req.DeviceType,
		Confidence: conf, Enabled: enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "fingerprint.create", "vendor_fingerprint", row.ID.String(), "Created "+row.Kind+" fingerprint", nil)
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) updateVendorFingerprint(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req fingerprintReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !fpKinds[req.Kind] {
		http.Error(w, "kind must be one of oid/service/port/http/ssh", http.StatusBadRequest)
		return
	}
	conf := int32(50)
	if req.Confidence != nil {
		conf = *req.Confidence
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.queries.UpdateVendorFingerprint(r.Context(), db.UpdateVendorFingerprintParams{
		ID: id, Kind: req.Kind, Pattern: req.Pattern, Vendor: req.Vendor, DeviceType: req.DeviceType,
		Confidence: conf, Enabled: enabled,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "fingerprint.update", "vendor_fingerprint", id.String(), "Updated fingerprint", nil)
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deleteVendorFingerprint(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteVendorFingerprint(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "fingerprint.delete", "vendor_fingerprint", id.String(), "Deleted fingerprint", nil)
	w.WriteHeader(http.StatusNoContent)
}

// ===== Audit log ============================================================

type auditDTO struct {
	ID         int64           `json:"id"`
	At         string          `json:"at"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	Category   string          `json:"category"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Summary    string          `json:"summary"`
	Details    json.RawMessage `json:"details"`
}

func (s *Server) listAuditLog(w http.ResponseWriter, r *http.Request) {
	limit := int32(200)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = int32(n)
		}
	}
	var cat *string
	if c := r.URL.Query().Get("category"); c != "" {
		cat = &c
	}
	rows, err := s.queries.ListAuditLog(r.Context(), db.ListAuditLogParams{Limit: limit, Category: cat})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]auditDTO, 0, len(rows))
	for _, a := range rows {
		out = append(out, auditDTO{
			ID: a.ID, At: a.At.Format("2006-01-02T15:04:05Z07:00"), Actor: a.Actor, Action: a.Action,
			Category: a.Category, EntityType: a.EntityType, EntityID: a.EntityID, Summary: a.Summary,
			Details: jsonOrEmpty(a.Details),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
