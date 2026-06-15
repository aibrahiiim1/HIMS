package api

// Endpoint Intelligence — Report Builder. A single POST endpoint builds a report
// over the OS-inventoried fleet from operator-chosen entity + filters + columns.
// All user-supplied VALUES are bound parameters (never string-concatenated);
// only whitelisted column/operator identifiers are interpolated, so the dynamic
// SQL is injection-safe. Reads are scoped to the requester's sites (+ optional
// ?site=). The same read model (eiModelCTE) backs the device entity, so report
// columns line up with the analytics tabs. CSV export is produced client-side
// from the returned rows; this endpoint stays a clean JSON read.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const eiReportMaxLimit = 5000
const eiReportDefaultLimit = 1000

// qb accumulates bound args so dynamic conditions can reference stable $N
// placeholders. $1 is always the location filter (text[] or NULL).
type qb struct{ args []any }

func newQB(loc any) *qb      { return &qb{args: []any{loc}} }
func (b *qb) p(v any) string { b.args = append(b.args, v); return fmt.Sprintf("$%d", len(b.args)) }

type eiReportReq struct {
	Entity  string         `json:"entity"`  // devices|software|processes|services|disks|nics
	Filters map[string]any `json:"filters"` // see applyDeviceFilters / child filters
	Columns []string       `json:"columns"` // device entity only; whitelisted keys
	Sort    string         `json:"sort"`    // whitelisted key
	Desc    bool           `json:"desc"`
	Limit   int            `json:"limit"`
	Site    string         `json:"site"` // optional location-id (alternative to ?site=)
}

// eiColumn maps a whitelisted device column key to its SQL expression over `ep`
// (the eiModelCTE row) and a human label for the CSV/table header.
type eiColumn struct{ expr, label string }

var eiDeviceColumns = map[string]eiColumn{
	"hostname":          {"COALESCE(NULLIF(name,''), hostname)", "Device"},
	"ip":                {"ip", "IP"},
	"site":              {"site_name", "Site"},
	"category":          {"category", "Category"},
	"os":                {"os_caption", "OS"},
	"os_build":          {"os_build", "Build"},
	"os_arch":           {"os_arch", "Arch"},
	"model":             {"eff_model", "Model"},
	"vendor":            {"eff_vendor", "Vendor"},
	"serial":            {"eff_serial", "Serial"},
	"cpu":               {"cpu_model", "CPU"},
	"cpu_cores":         {"cpu_cores", "Cores"},
	"ram_gb":            {"round(ram_total_bytes/1073741824.0, 1)", "RAM (GB)"},
	"disk_total_gb":     {"round(c_total/1073741824.0, 1)", "C: total (GB)"},
	"disk_free_gb":      {"round(min_free/1073741824.0, 1)", "Min free (GB)"},
	"disk_free_pct":     {"round(min_free_pct*100, 1)", "Min free %"},
	"software_count":    {"sw_count", "Software"},
	"process_count":     {"proc_count", "Processes"},
	"service_count":     {"svc_count", "Services"},
	"nic_count":         {"nic_count", "NICs"},
	"last_collected":    {"collected_at", "Last collected"},
	"collection_method": {"COALESCE(collection_method,'not collected')", "Method"},
	"management":        {"CASE WHEN collected THEN 'managed' ELSE 'unmanaged' END", "Management"},
	"domain":            {"NULLIF(domain,'')", "Domain"},
	"software_note":     {"NULLIF(software_note,'')", "Collection note"},
	"health_score":      {eiScoreExpr, "Health"},
}

// eiScoreExpr is the inline health score (same penalties as eiHealthScoreSelect)
// but with literal thresholds so the Report Builder needs no extra params.
const eiScoreExpr = `GREATEST(0, LEAST(100, 100
  - CASE WHEN NOT collected THEN 60 ELSE 0 END
  - CASE WHEN collected AND ram_total_bytes IS NOT NULL AND ram_total_bytes < 8589934592 THEN 15 ELSE 0 END
  - CASE WHEN collected AND ((min_free IS NOT NULL AND min_free < 10737418240) OR (c_free IS NOT NULL AND c_free < 10737418240)) THEN 15 ELSE 0 END
  - CASE WHEN os_caption ~* '(Windows 7|Windows XP|Windows Vista|Windows 8|Server 2003|Server 2008|Server 2012)' THEN 20 ELSE 0 END
  - CASE WHEN collected AND sw_count = 0 THEN 10 ELSE 0 END
  - CASE WHEN collected AND proc_count = 0 THEN 5 ELSE 0 END
  - CASE WHEN eff_serial IS NULL OR eff_model IS NULL THEN 5 ELSE 0 END
  - CASE WHEN cpu_cores IS NOT NULL AND cpu_cores <= 2 THEN 10 ELSE 0 END))::int`

func (s *Server) eiReportBuilder(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		http.Error(w, "analytics pool not configured", http.StatusServiceUnavailable)
		return
	}
	var req eiReportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 || req.Limit > eiReportMaxLimit {
		req.Limit = eiReportDefaultLimit
	}
	var explicit *uuid.UUID
	if req.Site != "" {
		if id, err := uuid.Parse(req.Site); err == nil {
			explicit = &id
		}
	}
	loc := eiLocArg(s.eiLocScope(r.Context(), explicit))

	var sql string
	var b *qb
	var cols []map[string]string
	switch req.Entity {
	case "", "devices":
		sql, b, cols = eiBuildDeviceReport(req, loc)
	case "software", "processes", "services", "disks", "nics":
		sql, b, cols = eiBuildChildReport(req, loc)
	default:
		http.Error(w, "unknown entity: "+req.Entity, http.StatusBadRequest)
		return
	}

	rows, err := eiRows(r.Context(), s.pool, sql, b.args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	truncated := len(rows) >= req.Limit
	writeJSON(w, http.StatusOK, map[string]any{
		"entity": orDefault(req.Entity, "devices"), "columns": cols,
		"rows": rows, "total": len(rows), "truncated": truncated, "limit": req.Limit,
	})
}

// eiBuildDeviceReport assembles the device-entity report: chosen columns over
// the read model + a parameterised WHERE from the filter set.
func eiBuildDeviceReport(req eiReportReq, loc any) (string, *qb, []map[string]string) {
	b := newQB(loc)

	// Columns — whitelist; device_id is always selected (hidden row key).
	keys := req.Columns
	if len(keys) == 0 {
		keys = []string{"hostname", "ip", "site", "os", "ram_gb", "disk_free_gb", "software_count", "process_count", "last_collected", "collection_method", "health_score"}
	}
	sel := []string{"device_id"}
	cols := []map[string]string{}
	for _, k := range keys {
		c, ok := eiDeviceColumns[k]
		if !ok {
			continue
		}
		sel = append(sel, fmt.Sprintf("%s AS %s", c.expr, k))
		cols = append(cols, map[string]string{"key": k, "label": c.label})
	}

	conds := eiDeviceConds(req.Filters, b)
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// Sort — whitelist; default health ascending (worst first).
	order := eiScoreExpr + " ASC"
	if c, ok := eiDeviceColumns[req.Sort]; ok {
		order = c.expr
		if req.Desc {
			order += " DESC NULLS LAST"
		} else {
			order += " ASC NULLS LAST"
		}
	}

	sql := eiModelCTE + "\nSELECT " + strings.Join(sel, ", ") + " FROM ep" + where +
		" ORDER BY " + order + fmt.Sprintf(" LIMIT %d", req.Limit)
	return sql, b, cols
}

// eiDeviceConds turns the filter map into parameterised SQL conditions over `ep`.
// Unknown keys are ignored. Every value is a bound param.
func eiDeviceConds(f map[string]any, b *qb) []string {
	conds := []string{}
	str := func(k string) (string, bool) {
		v, ok := f[k]
		if !ok || v == nil {
			return "", false
		}
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		return s, s != ""
	}
	numf := func(k string) (float64, bool) {
		v, ok := f[k]
		if !ok || v == nil {
			return 0, false
		}
		switch n := v.(type) {
		case float64:
			return n, true
		case string:
			var x float64
			if _, err := fmt.Sscanf(n, "%g", &x); err == nil {
				return x, true
			}
		}
		return 0, false
	}
	const gib = 1073741824.0

	if v, ok := str("category"); ok {
		conds = append(conds, "category = "+b.p(v))
	}
	if v, ok := str("management"); ok {
		if v == "managed" {
			conds = append(conds, "collected")
		} else if v == "unmanaged" {
			conds = append(conds, "NOT collected")
		}
	}
	if v, ok := str("collection_method"); ok {
		conds = append(conds, "collection_method = "+b.p(v))
	}
	if v, ok := str("os_contains"); ok {
		conds = append(conds, "os_caption ILIKE "+b.p("%"+v+"%"))
	}
	if v, ok := str("hostname_contains"); ok {
		conds = append(conds, "(name ILIKE "+b.p("%"+v+"%")+" OR hostname ILIKE "+b.p("%"+v+"%")+")")
	}
	if v, ok := str("model_contains"); ok {
		conds = append(conds, "eff_model ILIKE "+b.p("%"+v+"%"))
	}
	if v, ok := str("vendor_contains"); ok {
		conds = append(conds, "eff_vendor ILIKE "+b.p("%"+v+"%"))
	}
	if v, ok := str("cpu_contains"); ok {
		conds = append(conds, "cpu_model ILIKE "+b.p("%"+v+"%"))
	}
	if n, ok := numf("ram_lt_gb"); ok {
		conds = append(conds, "ram_total_bytes IS NOT NULL AND ram_total_bytes < "+b.p(int64(n*gib)))
	}
	if n, ok := numf("ram_gt_gb"); ok {
		conds = append(conds, "ram_total_bytes > "+b.p(int64(n*gib)))
	}
	if n, ok := numf("disk_free_lt_gb"); ok {
		conds = append(conds, "min_free IS NOT NULL AND min_free < "+b.p(int64(n*gib)))
	}
	if n, ok := numf("disk_free_lt_pct"); ok {
		conds = append(conds, "min_free_pct IS NOT NULL AND min_free_pct < "+b.p(n/100.0))
	}
	if n, ok := numf("collected_older_than_days"); ok {
		conds = append(conds, "collected AND collected_at < now() - ("+b.p(fmt.Sprintf("%g days", n))+"::interval)")
	}
	if v, ok := str("serial"); ok {
		if v == "missing" {
			conds = append(conds, "eff_serial IS NULL")
		} else if v == "exists" {
			conds = append(conds, "eff_serial IS NOT NULL")
		}
	}
	if v, ok := str("software_installed"); ok {
		conds = append(conds, "EXISTS (SELECT 1 FROM os_software s WHERE s.device_id::text = ep.device_id AND s.name ILIKE "+b.p("%"+v+"%")+")")
	}
	if v, ok := str("software_not_installed"); ok {
		conds = append(conds, "collected AND NOT EXISTS (SELECT 1 FROM os_software s WHERE s.device_id::text = ep.device_id AND s.name ILIKE "+b.p("%"+v+"%")+")")
	}
	if v, ok := str("process_running"); ok {
		conds = append(conds, "EXISTS (SELECT 1 FROM os_processes pr WHERE pr.device_id::text = ep.device_id AND pr.name ILIKE "+b.p("%"+v+"%")+")")
	}
	if v, ok := str("process_not_running"); ok {
		conds = append(conds, "collected AND NOT EXISTS (SELECT 1 FROM os_processes pr WHERE pr.device_id::text = ep.device_id AND pr.name ILIKE "+b.p("%"+v+"%")+")")
	}
	if v, ok := str("service_name"); ok {
		state, _ := str("service_state") // running|stopped (optional)
		extra := ""
		if state == "running" {
			extra = " AND sv.status IN ('Running','Started','OK')"
		} else if state == "stopped" {
			extra = " AND COALESCE(sv.status,'') NOT IN ('Running','Started','OK')"
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM os_services sv WHERE sv.device_id::text = ep.device_id AND sv.name ILIKE "+b.p("%"+v+"%")+extra+")")
	}
	if v, ok := str("ip_subnet"); ok {
		conds = append(conds, "device_id IN (SELECT id::text FROM devices WHERE deleted_at IS NULL AND primary_ip <<= "+b.p(v)+"::inet)")
	}
	if v, ok := str("old_os"); ok && (v == "true" || v == "1") {
		conds = append(conds, "os_caption ~* '(Windows 7|Windows XP|Windows Vista|Windows 8|Server 2003|Server 2008|Server 2012)'")
	}
	if v, ok := str("missing_software"); ok && (v == "true" || v == "1") {
		conds = append(conds, "collected AND sw_count = 0")
	}
	if v, ok := str("missing_processes"); ok && (v == "true" || v == "1") {
		conds = append(conds, "collected AND proc_count = 0")
	}
	return conds
}

// eiBuildChildReport handles the row-level entities (software/processes/services/
// disks/nics): a flat dump joined to the scoped device universe, with name /
// publisher / version contains filters and device/site context.
func eiBuildChildReport(req eiReportReq, loc any) (string, *qb, []map[string]string) {
	b := newQB(loc)
	f := req.Filters
	str := func(k string) (string, bool) {
		v, ok := f[k]
		if !ok || v == nil {
			return "", false
		}
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		return s, s != ""
	}

	var table, selectCols string
	var cols []map[string]string
	switch req.Entity {
	case "software":
		table = "os_software"
		selectCols = "x.name, x.version, COALESCE(x.publisher,'') AS publisher, COALESCE(x.arch,'') AS arch, COALESCE(x.install_date,'') AS install_date"
		cols = []map[string]string{{"key": "device", "label": "Device"}, {"key": "ip", "label": "IP"}, {"key": "site", "label": "Site"}, {"key": "name", "label": "Software"}, {"key": "version", "label": "Version"}, {"key": "publisher", "label": "Publisher"}, {"key": "arch", "label": "Arch"}, {"key": "install_date", "label": "Installed"}}
	case "processes":
		table = "os_processes"
		selectCols = "x.name, x.pid, x.mem_bytes"
		cols = []map[string]string{{"key": "device", "label": "Device"}, {"key": "ip", "label": "IP"}, {"key": "site", "label": "Site"}, {"key": "name", "label": "Process"}, {"key": "pid", "label": "PID"}, {"key": "mem_bytes", "label": "Memory (bytes)"}}
	case "services":
		table = "os_services"
		selectCols = "x.name, COALESCE(x.display_name,'') AS display_name, COALESCE(x.status,'') AS status, COALESCE(x.start_type,'') AS start_type"
		cols = []map[string]string{{"key": "device", "label": "Device"}, {"key": "ip", "label": "IP"}, {"key": "site", "label": "Site"}, {"key": "name", "label": "Service"}, {"key": "display_name", "label": "Display name"}, {"key": "status", "label": "Status"}, {"key": "start_type", "label": "Startup"}}
	case "disks":
		table = "os_disks"
		selectCols = "x.name, COALESCE(x.filesystem,'') AS filesystem, x.total_bytes, x.free_bytes"
		cols = []map[string]string{{"key": "device", "label": "Device"}, {"key": "ip", "label": "IP"}, {"key": "site", "label": "Site"}, {"key": "name", "label": "Drive"}, {"key": "filesystem", "label": "FS"}, {"key": "total_bytes", "label": "Total (bytes)"}, {"key": "free_bytes", "label": "Free (bytes)"}}
	case "nics":
		table = "os_nics"
		selectCols = "x.name, COALESCE(x.mac,'') AS mac, COALESCE(x.ip_addresses,'') AS ip_addresses, x.link_speed_mbps"
		cols = []map[string]string{{"key": "device", "label": "Device"}, {"key": "ip", "label": "IP"}, {"key": "site", "label": "Site"}, {"key": "name", "label": "NIC"}, {"key": "mac", "label": "MAC"}, {"key": "ip_addresses", "label": "Addresses"}, {"key": "link_speed_mbps", "label": "Link (Mbps)"}}
	}

	conds := []string{"d.deleted_at IS NULL",
		"($1::text[] IS NULL OR d.location_id::text = ANY($1::text[]))"}
	if v, ok := str("name_contains"); ok {
		conds = append(conds, "x.name ILIKE "+b.p("%"+v+"%"))
	}
	if req.Entity == "software" {
		if v, ok := str("publisher_contains"); ok {
			conds = append(conds, "x.publisher ILIKE "+b.p("%"+v+"%"))
		}
		if v, ok := str("version"); ok {
			conds = append(conds, "x.version = "+b.p(v))
		}
	}
	if v, ok := str("site"); ok {
		conds = append(conds, "lo.name ILIKE "+b.p("%"+v+"%"))
	}

	order := "d.name, x.name"
	if req.Entity == "processes" {
		order = "x.mem_bytes DESC NULLS LAST"
	}
	sql := "SELECT d.id::text AS device_id, d.name AS device, host(d.primary_ip) AS ip, lo.name AS site, " +
		selectCols + " FROM " + table + " x " +
		"JOIN devices d ON d.id = x.device_id LEFT JOIN locations lo ON lo.id = d.location_id " +
		"WHERE " + strings.Join(conds, " AND ") +
		" ORDER BY " + order + fmt.Sprintf(" LIMIT %d", req.Limit)
	return sql, b, cols
}
