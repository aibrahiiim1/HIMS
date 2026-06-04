package api

import (
	"net/http"
	"strings"
)

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
	case "credentials", "credential-groups", "security": // security = encryption
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
		// dashboard, system, data-quality, search, sites, assets, locations,
		// subnets, mibs, roles (CMDB), auth/* — authenticated-only.
		return ""
	}
}
