package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestRequiredPermission(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		// Sensitive resources — gated for all methods.
		{http.MethodGet, "/api/v1/credentials", "credentials.manage"},
		{http.MethodPost, "/api/v1/credentials", "credentials.manage"},
		{http.MethodGet, "/api/v1/rbac/users", "rbac.manage"},
		{http.MethodGet, "/api/v1/audit-log", "audit.read"},
		{http.MethodGet, "/api/v1/audit-log/export", "audit.read"},
		{http.MethodPost, "/api/v1/settings", "settings.manage"},
		{http.MethodGet, "/api/v1/admin/backup/export", "rbac.manage"},
		{http.MethodGet, "/api/v1/security/encryption/status", "credentials.manage"},

		// Devices: read vs write.
		{http.MethodGet, "/api/v1/devices", "devices.read"},
		{http.MethodPost, "/api/v1/devices", "devices.write"},
		{http.MethodPut, "/api/v1/devices/abc/lifecycle", "devices.write"},
		{http.MethodGet, "/api/v1/inventory", "devices.read"},

		// Discovery writes need discovery.run.
		{http.MethodPost, "/api/v1/discovery/scan", "discovery.run"},
		{http.MethodGet, "/api/v1/discovery/jobs", "devices.read"},

		// Alerts: ack vs manage vs read.
		{http.MethodGet, "/api/v1/alerts", "alerts.read"},
		{http.MethodPost, "/api/v1/alerts/abc/ack", "alerts.ack"},
		{http.MethodPost, "/api/v1/alerts/abc/resolve", "alerts.ack"},
		{http.MethodPost, "/api/v1/alerts/evaluate", "alerts.manage"},
		{http.MethodPost, "/api/v1/maintenance-windows", "alerts.manage"},

		// Work orders.
		{http.MethodGet, "/api/v1/work-orders", "work_orders.read"},
		{http.MethodPost, "/api/v1/work-orders", "work_orders.manage"},

		// Config backup, reports, monitoring.
		{http.MethodPost, "/api/v1/devices/abc/config-backups", "devices.write"}, // device sub-resource → devices
		{http.MethodGet, "/api/v1/config/overview", "config.backup"},
		{http.MethodGet, "/api/v1/reports/inventory/export", "reports.view"},
		{http.MethodPost, "/api/v1/report-schedules", "reports.schedule"},
		{http.MethodGet, "/api/v1/netflow/overview", "monitoring.read"},

		// Authenticated-only (no specific permission).
		{http.MethodGet, "/api/v1/dashboard", ""},
		{http.MethodGet, "/api/v1/system/runtime", ""},
		{http.MethodGet, "/api/v1/sites/overview", ""},
		{http.MethodGet, "/api/v1/auth/me", ""},
		{http.MethodGet, "/api/v1/search", ""},
	}
	for _, c := range cases {
		if got := requiredPermission(c.method, c.path); got != c.want {
			t.Errorf("requiredPermission(%s %s) = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

func TestInScope(t *testing.T) {
	group := uuid.New()
	chr := uuid.New()
	cac := uuid.New()
	room := uuid.New() // under CHR
	parent := map[uuid.UUID]uuid.UUID{chr: group, cac: group, room: chr}

	// Global user (nil site) sees everything, including unassigned.
	if !inScope(nil, &chr, parent) || !inScope(nil, nil, parent) {
		t.Error("global user must see all devices")
	}
	// CHR-scoped user sees CHR devices + devices in CHR's subtree (room).
	if !inScope(&chr, &chr, parent) {
		t.Error("CHR user should see a device located at CHR")
	}
	if !inScope(&chr, &room, parent) {
		t.Error("CHR user should see a device in a room under CHR")
	}
	// ...but NOT CAC devices, nor unassigned.
	if inScope(&chr, &cac, parent) {
		t.Error("CHR user must NOT see a CAC device")
	}
	if inScope(&chr, nil, parent) {
		t.Error("site-scoped user must NOT see unassigned devices")
	}
	// Group-scoped user sees both hotels.
	if !inScope(&group, &chr, parent) || !inScope(&group, &cac, parent) {
		t.Error("group user should see both hotels")
	}
}

func TestDeviceIDFromPath(t *testing.T) {
	id := uuid.New()
	if got, ok := deviceIDFromPath("/api/v1/devices/" + id.String() + "/interfaces"); !ok || got != id {
		t.Errorf("device sub-resource path → %v %v, want %v true", got, ok, id)
	}
	if got, ok := deviceIDFromPath("/api/v1/devices/" + id.String()); !ok || got != id {
		t.Errorf("device item path → %v %v", got, ok)
	}
	// Non-UUID device segments (list/bulk) are not single-device access.
	for _, p := range []string{"/api/v1/devices", "/api/v1/devices/bulk-delete", "/api/v1/devices/import-csv", "/api/v1/alerts/x"} {
		if _, ok := deviceIDFromPath(p); ok {
			t.Errorf("%q should not yield a device id", p)
		}
	}
}

func TestIdentityCan(t *testing.T) {
	viewer := &identity{Perms: map[string]bool{"devices.read": true}}
	if !viewer.can("devices.read") {
		t.Error("viewer should have devices.read")
	}
	if viewer.can("devices.write") {
		t.Error("viewer must not have devices.write")
	}
	admin := &identity{Admin: true}
	if !admin.can("anything.at.all") {
		t.Error("admin must bypass all permission checks")
	}
	// nil identity = open mode → allowed.
	var none *identity
	if !none.can("devices.write") {
		t.Error("nil identity (open mode) should allow")
	}
}
