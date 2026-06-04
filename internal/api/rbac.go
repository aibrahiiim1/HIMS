package api

import (
	"net/http"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// RBAC (#23) — permission matrix + a standard permission catalog.

// standardPermissions is the built-in catalog an operator imports once, so the
// role×permission matrix has meaningful codes to grant. Codes are stable and
// map to the app's functional areas.
var standardPermissions = []struct{ code, desc string }{
	{"devices.read", "View inventory and device detail"},
	{"devices.write", "Edit, classify, delete devices"},
	{"discovery.run", "Run network discovery scans + imports"},
	{"monitoring.read", "View monitoring + availability"},
	{"alerts.read", "View alerts"},
	{"alerts.ack", "Acknowledge / resolve alerts"},
	{"alerts.manage", "Manage alert rules + maintenance windows"},
	{"topology.read", "View topology + path finder"},
	{"work_orders.read", "View work orders"},
	{"work_orders.manage", "Create / update / close work orders"},
	{"config.backup", "Capture + view device config backups"},
	{"reports.view", "View + export reports"},
	{"reports.schedule", "Manage scheduled reports"},
	{"credentials.manage", "Manage credentials + encryption"},
	{"templates.manage", "Manage device templates + fingerprints"},
	{"rbac.manage", "Manage users, roles and permissions"},
	{"settings.manage", "Change system settings"},
	{"audit.read", "View the audit trail"},
}

// seedPermissions handles POST /rbac/permissions/seed — imports the standard
// catalog, skipping codes that already exist. Idempotent.
func (s *Server) seedPermissions(w http.ResponseWriter, r *http.Request) {
	existing, err := s.queries.ListPermissions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	have := make(map[string]bool, len(existing))
	for _, p := range existing {
		have[p.Code] = true
	}
	created, skipped := 0, 0
	for _, p := range standardPermissions {
		if have[p.code] {
			skipped++
			continue
		}
		if _, err := s.queries.CreatePermission(r.Context(), db.CreatePermissionParams{Code: p.code, Description: p.desc}); err != nil {
			writeErr(w, err)
			return
		}
		created++
	}
	s.audit(r, "user", "permission.seed", "permission", "", "Seeded standard permission catalog", map[string]any{"created": created, "skipped": skipped})
	writeJSON(w, http.StatusOK, map[string]int{"created": created, "skipped": skipped, "catalog_size": len(standardPermissions)})
}

// rbacMatrix handles GET /rbac/matrix — roles, permissions, and the grant set
// (which permission ids each role holds) in one payload for the matrix editor.
func (s *Server) rbacMatrix(w http.ResponseWriter, r *http.Request) {
	roles, err := s.queries.ListRoles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	perms, err := s.queries.ListPermissions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	grants := make(map[string][]string, len(roles))
	for _, role := range roles {
		rp, err := s.queries.PermissionsForRole(r.Context(), role.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		ids := make([]string, len(rp))
		for i, p := range rp {
			ids[i] = p.ID.String()
		}
		grants[role.ID.String()] = ids
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "permissions": perms, "grants": grants})
}
