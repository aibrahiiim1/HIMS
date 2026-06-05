package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// inScope reports whether a device (at deviceLoc) is within a user's site
// scope. A nil userSite is global (sees everything). A site-scoped user sees a
// device only if their site is the device's location or an ancestor of it
// (walking up the locations tree). Unassigned devices (nil location) are not
// visible to a site-scoped user. Pure + cycle-guarded → unit-tested.
func inScope(userSite, deviceLoc *uuid.UUID, parent map[uuid.UUID]uuid.UUID) bool {
	if userSite == nil {
		return true
	}
	if deviceLoc == nil {
		return false
	}
	cur := *deviceLoc
	for i := 0; i < 64; i++ {
		if cur == *userSite {
			return true
		}
		next, ok := parent[cur]
		if !ok || next == uuid.Nil || next == cur {
			return false
		}
		cur = next
	}
	return false
}

// deviceIDFromPath extracts a device UUID from a "/api/v1/devices/{id}/..."
// path. Non-UUID second segments (import-csv, bulk-delete, collection-summary)
// return false — those are list/bulk operations, not single-device access.
func deviceIDFromPath(path string) (uuid.UUID, bool) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1"), "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "devices" {
			if id, err := uuid.Parse(parts[i+1]); err == nil {
				return id, true
			}
			return uuid.Nil, false
		}
	}
	return uuid.Nil, false
}

// requiredPermission maps an HTTP method + /api/v1 path to the permission code
// a caller must hold. "" means "any authenticated user" (dashboards, system,
// search, site rollups, locations, MIBs, auth/me — viewing surfaces). It is
// pure so the authorization policy is unit-tested. An admin (rbac.manage)
// bypasses all checks; the bootstrap admin holds every permission.
func requiredPermission(method, path string) string {
	p := strings.TrimPrefix(path, "/api/v1/")
	p = strings.TrimPrefix(p, "/")
	tag := p
	if i := strings.IndexByte(tag, '/'); i >= 0 {
		tag = tag[:i]
	}
	write := method != http.MethodGet

	switch tag {
	// --- sensitive: same permission for read + write ---
	case "credentials", "credential-groups", "vendor-profiles", "security": // security = encryption
		return "credentials.manage"
	case "rbac":
		return "rbac.manage"
	case "audit-log":
		return "audit.read"
	case "settings":
		return "settings.manage"
	case "admin": // backup/export, dr-readiness, restore
		return "rbac.manage"

	// --- operational: read perm vs write perm ---
	case "discovery":
		if write {
			return "discovery.run"
		}
		return "devices.read"
	case "devices", "inventory":
		if write {
			return "devices.write"
		}
		return "devices.read"
	case "data-quality":
		// Reading the report is open to any authenticated user; the
		// reconcile-sites quick-action mutates device locations.
		if write {
			return "devices.write"
		}
		return ""
	case "device-templates", "vendor-fingerprints":
		if write {
			return "templates.manage"
		}
		return "" // viewing templates/fingerprints is open to any authenticated user
	case "alerts", "maintenance-windows", "notification-channels":
		if write {
			if strings.Contains(p, "/ack") || strings.Contains(p, "/resolve") || strings.Contains(p, "/note") {
				return "alerts.ack"
			}
			return "alerts.manage"
		}
		return "alerts.read"
	case "work-orders":
		if write {
			return "work_orders.manage"
		}
		return "work_orders.read"
	case "systems", "spare-parts", "expenses":
		if write {
			return "work_orders.manage"
		}
		return ""
	case "config-backups", "config":
		return "config.backup"
	case "report-schedules":
		return "reports.schedule"
	case "reports":
		return "reports.view"
	case "monitoring", "netflow":
		return "monitoring.read"
	case "topology":
		return "topology.read"

	default:
		// dashboard, system, search, sites, assets, locations,
		// subnets, mibs, roles (CMDB), auth/* — authenticated-only.
		return ""
	}
}
