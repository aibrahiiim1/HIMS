package api

import "net/http"

// dashboard assembles the executive rollup in one call: inventory by
// category/status, monitoring health, and operations headline counts. Each
// piece is best-effort — a query error degrades to a zero/empty section
// rather than failing the whole dashboard (so an empty DB still renders).
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{}

	if rows, err := s.queries.DeviceCountByCategory(ctx); err == nil {
		out["by_category"] = rows
	}
	if rows, err := s.queries.DeviceCountByStatus(ctx); err == nil {
		out["by_status"] = rows
	}
	if rows, err := s.queries.RoleSummary(ctx); err == nil {
		out["by_role"] = rows
	}
	if rows, err := s.queries.MonitoringStatusOverview(ctx); err == nil {
		out["monitoring"] = rows
	}
	if rows, err := s.queries.ExpensesByCategory(ctx); err == nil {
		out["expenses_by_category"] = rows
	}

	headline := map[string]any{}
	if n, err := s.queries.CountOpenWorkOrders(ctx); err == nil {
		headline["open_work_orders"] = n
	}
	if n, err := s.queries.CountOpenAlerts(ctx); err == nil {
		headline["open_alerts"] = n
	}
	if n, err := s.queries.CountExpiringSystems(ctx); err == nil {
		headline["expiring_systems"] = n
	}
	if n, err := s.queries.CountDevicesNeedingAttention(ctx); err == nil {
		headline["devices_needing_attention"] = n
	}
	if v, err := s.queries.TotalExpenses(ctx); err == nil {
		headline["total_expenses"] = v
	}
	if n, err := s.queries.CountVirtualDevices(ctx); err == nil {
		headline["virtual_devices"] = n // "N devices, M virtual"
	}
	out["headline"] = headline

	writeJSON(w, http.StatusOK, out)
}
