// Package api — Endpoint Intelligence: a technical analytics + reporting center
// for the OS-inventoried fleet (Windows workstations/laptops/servers, Linux
// hosts, and peripherals where applicable). Every figure is grounded in real
// persisted inventory (devices + os_inventory + os_disks/nics/services/
// processes/software). Nothing is fabricated: a missing value reads as "not
// collected", and where collection failed the precise reason (software_note,
// stale collected_at, absent child rows) is surfaced rather than hidden.
//
// All endpoints are read-only aggregate SQL over a single reusable per-device
// read model (eiModelCTE). They are far more practical as raw pgx than as
// generated sqlc queries, and the same pool powers the dynamic Report Builder.
// Site scope is honoured: a scoped user only ever sees devices in their subtree,
// and an optional ?site=<location_id> narrows further.
package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// eiModelCTE is the shared per-device read model. $1 is a text[] of allowed
// location ids (NULL = unrestricted). It pre-aggregates the child-table counts
// every section needs (software/process/service/disk/nic) plus the C: drive and
// the "effective" serial/model/vendor (os_inventory first, device record as
// fallback). The endpoint universe is OS-inventoried hosts + endpoint/server
// devices — i.e. workstations, laptops and servers, never switches/cameras.
const eiModelCTE = `
WITH ep AS (
  SELECT d.id::text AS device_id, d.name, COALESCE(d.hostname,'') AS hostname,
         host(d.primary_ip) AS ip, d.location_id::text AS location_id,
         d.category, d.status, d.os_family, d.is_virtual,
         (d.credential_id IS NOT NULL) AS has_cred,
         oi.device_id IS NOT NULL AS collected,
         oi.collected_at, oi.collection_method, COALESCE(oi.software_note,'') AS software_note,
         oi.os_caption, oi.os_version, oi.os_build, oi.os_arch,
         COALESCE(oi.domain,'') AS domain, COALESCE(oi.workgroup,'') AS workgroup,
         oi.cpu_model, oi.cpu_cores, oi.cpu_sockets, oi.ram_total_bytes,
         oi.last_boot, oi.uptime_seconds,
         NULLIF(COALESCE(NULLIF(oi.serial,''), NULLIF(d.serial,'')),'') AS eff_serial,
         NULLIF(COALESCE(NULLIF(oi.model,''), NULLIF(d.model,'')),'') AS eff_model,
         NULLIF(COALESCE(NULLIF(oi.manufacturer,''), NULLIF(d.vendor,'')),'') AS eff_vendor,
         COALESCE(sw.n,0) AS sw_count, COALESCE(pr.n,0) AS proc_count,
         COALESCE(sv.n,0) AS svc_count, COALESCE(sv.auto_stopped,0) AS auto_stopped,
         COALESCE(dk.n,0) AS disk_count, dk.min_free, dk.min_free_pct,
         cd.c_free, cd.c_total, COALESCE(nc.n,0) AS nic_count, lo.name AS site_name
  FROM devices d
  LEFT JOIN os_inventory oi ON oi.device_id = d.id
  LEFT JOIN locations lo ON lo.id = d.location_id
  LEFT JOIN (SELECT device_id, count(*) n FROM os_software GROUP BY 1) sw ON sw.device_id = d.id
  LEFT JOIN (SELECT device_id, count(*) n FROM os_processes GROUP BY 1) pr ON pr.device_id = d.id
  LEFT JOIN (SELECT device_id, count(*) n,
                    count(*) FILTER (WHERE start_type ILIKE 'Auto%'
                                       AND COALESCE(status,'') NOT IN ('Running','Started','OK','Auto')) AS auto_stopped
             FROM os_services GROUP BY 1) sv ON sv.device_id = d.id
  LEFT JOIN (SELECT device_id, count(*) n, min(free_bytes) min_free,
                    min(CASE WHEN total_bytes > 0 THEN free_bytes::float8/total_bytes ELSE NULL END) min_free_pct
             FROM os_disks GROUP BY 1) dk ON dk.device_id = d.id
  LEFT JOIN (SELECT DISTINCT ON (device_id) device_id, free_bytes c_free, total_bytes c_total
             FROM os_disks WHERE name ILIKE 'C:%' ORDER BY device_id, total_bytes DESC NULLS LAST) cd ON cd.device_id = d.id
  LEFT JOIN (SELECT device_id, count(*) n FROM os_nics GROUP BY 1) nc ON nc.device_id = d.id
  WHERE d.deleted_at IS NULL
    AND (d.category IN ('endpoint','server') OR d.os_family IN ('windows','linux') OR oi.device_id IS NOT NULL)
    AND ($1::text[] IS NULL OR d.location_id::text = ANY($1::text[]))
)`

// eiThresholds are the operator-tunable knobs (query params, with the documented
// defaults). They flow into the WHERE/FILTER clauses as bound params so the same
// SQL serves any threshold without string-building.
type eiThresholds struct {
	lowRAMBytes  int64   // low_ram_gb
	diskFreeByte int64   // disk_free_gb
	diskFreeFrac float64 // disk_free_pct (0-1)
	cFreeBytes   int64   // c_free_gb
	staleDays    int     // stale_days
	autoStopMin  int     // many_stopped_services threshold
}

func eiParseThresholds(r *http.Request) eiThresholds {
	const gib = 1024 * 1024 * 1024
	q := r.URL.Query()
	fnum := func(key string, def float64) float64 {
		if v := q.Get(key); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				return f
			}
		}
		return def
	}
	inum := func(key string, def int) int {
		if v := q.Get(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				return n
			}
		}
		return def
	}
	return eiThresholds{
		lowRAMBytes:  int64(fnum("low_ram_gb", 8) * gib),
		diskFreeByte: int64(fnum("disk_free_gb", 10) * gib),
		diskFreeFrac: fnum("disk_free_pct", 15) / 100.0,
		cFreeBytes:   int64(fnum("c_free_gb", 10) * gib),
		staleDays:    inum("stale_days", 7),
		autoStopMin:  inum("auto_stopped_min", 5),
	}
}

func (t eiThresholds) json() map[string]any {
	const gib = 1024 * 1024 * 1024
	return map[string]any{
		"low_ram_gb":    t.lowRAMBytes / gib,
		"disk_free_gb":  t.diskFreeByte / gib,
		"disk_free_pct": int(t.diskFreeFrac*100 + 0.5),
		"c_free_gb":     t.cFreeBytes / gib,
		"stale_days":    t.staleDays,
	}
}

// eiLocScope resolves the location-id filter for the requester: the intersection
// of the requester's hard site subtree (nil = global) and an optional ?site=
// subtree. Returns nil when unrestricted, else the list of allowed location ids
// (possibly empty = matches nothing). Descendants are walked so a hotel scope
// includes its buildings/floors/rooms.
func (s *Server) eiLocScope(ctx context.Context, explicit *uuid.UUID) []uuid.UUID {
	parents := s.locationParents(ctx) // child -> parent
	kids := map[uuid.UUID][]uuid.UUID{}
	for c, p := range parents {
		kids[p] = append(kids[p], c)
	}
	descend := func(root uuid.UUID) map[uuid.UUID]bool {
		seen := map[uuid.UUID]bool{}
		stack := []uuid.UUID{root}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[n] {
				continue
			}
			seen[n] = true
			stack = append(stack, kids[n]...)
		}
		return seen
	}
	var req, exp map[uuid.UUID]bool
	if id, ok := identityFrom(ctx); ok && id != nil && id.SiteID != nil {
		req = descend(*id.SiteID)
	}
	if explicit != nil {
		exp = descend(*explicit)
	}
	if req == nil && exp == nil {
		return nil
	}
	base := exp
	if base == nil {
		base = req
	}
	out := make([]uuid.UUID, 0, len(base))
	for k := range base {
		if (req == nil || req[k]) && (exp == nil || exp[k]) {
			out = append(out, k)
		}
	}
	return out
}

// eiLocArg turns the scope into a $1 param value: nil (SQL NULL = unrestricted)
// or a []string of location-id texts (possibly empty = match nothing).
func eiLocArg(scope []uuid.UUID) any {
	if scope == nil {
		return nil
	}
	out := make([]string, len(scope))
	for i, id := range scope {
		out[i] = id.String()
	}
	return out
}

// eiSite parses the optional ?site=<uuid> query param.
func eiSite(r *http.Request) *uuid.UUID {
	if v := r.URL.Query().Get("site"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			return &id
		}
	}
	return nil
}

// --- generic row scanners (JSON keys == SQL column aliases) -------------------

func eiRows(ctx context.Context, p pgConn, sql string, args ...any) ([]map[string]any, error) {
	rows, err := p.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fds := rows.FieldDescriptions()
	out := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(fds))
		for i, fd := range fds {
			m[string(fd.Name)] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func eiRow(ctx context.Context, p pgConn, sql string, args ...any) (map[string]any, error) {
	rows, err := eiRows(ctx, p, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return map[string]any{}, nil
	}
	return rows[0], nil
}

// pgConn is the minimal query surface (satisfied by *pgxpool.Pool) — keeps the
// scanners testable without a live pool.
type pgConn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// eiGuard returns the pool + the resolved $1 location arg, or writes 503/err.
func (s *Server) eiGuard(w http.ResponseWriter, r *http.Request) (pgConn, any, bool) {
	if s.pool == nil {
		http.Error(w, "analytics pool not configured", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	scope := s.eiLocScope(r.Context(), eiSite(r))
	return s.pool, eiLocArg(scope), true
}

// ---- 1. Overview -------------------------------------------------------------

func (s *Server) eiOverview(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	t := eiParseThresholds(r)
	staleIv := strconv.Itoa(t.staleDays) + " days"

	cards, err := eiRow(ctx, p, eiModelCTE+`
SELECT
  count(*) AS total,
  count(*) FILTER (WHERE collected) AS managed,
  count(*) FILTER (WHERE NOT collected) AS unmanaged,
  count(*) FILTER (WHERE category='server') AS servers,
  count(*) FILTER (WHERE category='endpoint') AS workstations,
  count(*) FILTER (WHERE eff_model ~* '(book|laptop|notebook|thinkpad|latitude|inspiron|ideapad|probook|elitebook|surface)') AS laptops,
  count(*) FILTER (WHERE ram_total_bytes IS NOT NULL AND ram_total_bytes < $2::bigint) AS low_ram,
  count(*) FILTER (WHERE (min_free IS NOT NULL AND min_free < $3::bigint)
                      OR (c_free IS NOT NULL AND c_free < $5::bigint)
                      OR (min_free_pct IS NOT NULL AND min_free_pct < $4::float8)) AS low_free_disk,
  count(*) FILTER (WHERE collected AND sw_count=0) AS missing_software,
  count(*) FILTER (WHERE collected AND proc_count=0) AS missing_processes,
  count(*) FILTER (WHERE collected AND collected_at < now() - ($6::text)::interval) AS stale_collected,
  count(*) FILTER (WHERE os_caption ~* '(Windows 7|Windows XP|Windows Vista|Windows 8|Server 2003|Server 2008|Server 2012)') AS old_os,
  count(*) FILTER (WHERE auto_stopped >= $7::int) AS many_stopped_services,
  count(*) FILTER (WHERE eff_serial IS NULL OR eff_model IS NULL) AS missing_serial_model,
  count(*) FILTER (WHERE collected AND (software_note ~* '(denied|disabled|unreachable|failed|unsupported|no_software)' OR sw_count=0 OR proc_count=0)) AS collection_warnings
FROM ep`,
		loc, t.lowRAMBytes, t.diskFreeByte, t.diskFreeFrac, t.cFreeBytes, staleIv, t.autoStopMin)
	if err != nil {
		writeErr(w, err)
		return
	}

	dupHost, err := eiRow(ctx, p, eiModelCTE+`
SELECT count(*) AS duplicate_hostnames FROM (
  SELECT lower(hostname) h FROM ep WHERE hostname <> '' GROUP BY 1 HAVING count(*) > 1
) x`, loc)
	if err != nil {
		writeErr(w, err)
		return
	}
	cards["duplicate_hostnames"] = dupHost["duplicate_hostnames"]

	// Worst / best devices by health score (computed in SQL, see eiHealthScoreExpr).
	worst, err := eiRows(ctx, p, eiModelCTE+`, scored AS (`+eiHealthScoreSelect+`)
SELECT * FROM scored ORDER BY health_score ASC, ram_total_bytes ASC NULLS FIRST LIMIT 10`,
		loc, t.lowRAMBytes, t.diskFreeByte, t.cFreeBytes, staleIv)
	if err != nil {
		writeErr(w, err)
		return
	}
	best, err := eiRows(ctx, p, eiModelCTE+`, scored AS (`+eiHealthScoreSelect+`)
SELECT * FROM scored WHERE collected ORDER BY health_score DESC, ram_total_bytes DESC NULLS LAST LIMIT 10`,
		loc, t.lowRAMBytes, t.diskFreeByte, t.cFreeBytes, staleIv)
	if err != nil {
		writeErr(w, err)
		return
	}
	dist, err := eiRows(ctx, p, eiModelCTE+`, scored AS (`+eiHealthScoreSelect+`)
SELECT bucket, count(*) AS count FROM (
  SELECT CASE WHEN health_score >= 80 THEN '80-100'
              WHEN health_score >= 60 THEN '60-79'
              WHEN health_score >= 40 THEN '40-59'
              WHEN health_score >= 20 THEN '20-39'
              ELSE '0-19' END AS bucket
  FROM scored
) b GROUP BY bucket ORDER BY bucket`,
		loc, t.lowRAMBytes, t.diskFreeByte, t.cFreeBytes, staleIv)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"thresholds":               t.json(),
		"cards":                    cards,
		"worst_devices":            worst,
		"best_devices":             best,
		"health_score_distribution": dist,
	})
}

// eiHealthScoreSelect computes a 0-100 endpoint health score with documented
// penalties. Params (after the CTE's $1): $2 lowRAMBytes, $3 diskFreeBytes,
// $4 cFreeBytes, $5 stale interval text. Uncollected devices score low (they
// can't be assessed) rather than fake-perfect.
const eiHealthScoreSelect = `
SELECT device_id, name, hostname, ip, location_id, site_name, category, status, collected,
       collected_at, collection_method, software_note, os_caption, os_arch,
       eff_model, eff_vendor, eff_serial, cpu_model, cpu_cores, ram_total_bytes,
       disk_count, min_free, c_free, c_total, sw_count, proc_count, svc_count, nic_count, is_virtual,
       GREATEST(0, LEAST(100, 100
         - CASE WHEN NOT collected THEN 60 ELSE 0 END
         - CASE WHEN collected AND ram_total_bytes IS NOT NULL AND ram_total_bytes < $2::bigint THEN 15 ELSE 0 END
         - CASE WHEN collected AND ((min_free IS NOT NULL AND min_free < $3::bigint) OR (c_free IS NOT NULL AND c_free < $4::bigint)) THEN 15 ELSE 0 END
         - CASE WHEN os_caption ~* '(Windows 7|Windows XP|Windows Vista|Windows 8|Server 2003|Server 2008|Server 2012)' THEN 20 ELSE 0 END
         - CASE WHEN collected AND sw_count = 0 THEN 10 ELSE 0 END
         - CASE WHEN collected AND proc_count = 0 THEN 5 ELSE 0 END
         - CASE WHEN collected AND collected_at < now() - ($5::text)::interval THEN 10 ELSE 0 END
         - CASE WHEN eff_serial IS NULL OR eff_model IS NULL THEN 5 ELSE 0 END
         - CASE WHEN cpu_cores IS NOT NULL AND cpu_cores <= 2 THEN 10 ELSE 0 END
       ))::int AS health_score
FROM ep`

// ---- 2. Hardware -------------------------------------------------------------

func (s *Server) eiHardware(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	var err error

	if out["ram_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT bucket, count(*) AS count FROM (
  SELECT CASE WHEN ram_total_bytes IS NULL THEN 'unknown'
              WHEN ram_total_bytes < 4::bigint*1073741824 THEN '< 4 GB'
              WHEN ram_total_bytes < 8::bigint*1073741824 THEN '4-8 GB'
              WHEN ram_total_bytes < 16::bigint*1073741824 THEN '8-16 GB'
              WHEN ram_total_bytes < 32::bigint*1073741824 THEN '16-32 GB'
              ELSE '> 32 GB' END AS bucket FROM ep
) b GROUP BY bucket ORDER BY min(CASE bucket WHEN '< 4 GB' THEN 0 WHEN '4-8 GB' THEN 1 WHEN '8-16 GB' THEN 2 WHEN '16-32 GB' THEN 3 WHEN '> 32 GB' THEN 4 ELSE 5 END)`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["cpu_models"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(NULLIF(trim(cpu_model),''),'unknown') AS label, count(*) AS count
FROM ep GROUP BY 1 ORDER BY count DESC, label LIMIT 40`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["cpu_vendors"], err = eiRows(ctx, p, eiModelCTE+`
SELECT CASE WHEN cpu_model ~* 'intel' THEN 'Intel'
            WHEN cpu_model ~* 'amd|ryzen|epyc|opteron' THEN 'AMD'
            WHEN cpu_model ~* 'arm|apple|qualcomm' THEN 'ARM'
            WHEN cpu_model IS NULL OR trim(cpu_model)='' THEN 'unknown'
            ELSE 'other' END AS label, count(*) AS count
FROM ep GROUP BY 1 ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["cpu_cores"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(cpu_cores::text,'unknown') AS label, count(*) AS count
FROM ep GROUP BY cpu_cores ORDER BY cpu_cores NULLS LAST`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["models"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(eff_model,'unknown') AS label, count(*) AS count
FROM ep GROUP BY 1 ORDER BY count DESC, label LIMIT 40`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["vendors"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(eff_vendor,'unknown') AS label, count(*) AS count
FROM ep GROUP BY 1 ORDER BY count DESC, label LIMIT 40`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["os_arch"], err = eiRows(ctx, p, eiModelCTE+`
SELECT CASE WHEN os_arch ~* '64' THEN '64-bit'
            WHEN os_arch ~* '32|x86' THEN '32-bit'
            WHEN os_arch IS NULL OR trim(os_arch)='' THEN 'unknown'
            ELSE os_arch END AS label, count(*) AS count
FROM ep GROUP BY 1 ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["virtual"], err = eiRows(ctx, p, eiModelCTE+`
SELECT CASE WHEN is_virtual THEN 'virtual' ELSE 'physical' END AS label, count(*) AS count
FROM ep GROUP BY 1 ORDER BY label`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["lowest_ram"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, eff_model, cpu_model, ram_total_bytes, os_caption
FROM ep WHERE ram_total_bytes IS NOT NULL ORDER BY ram_total_bytes ASC LIMIT 15`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["highest_ram"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, eff_model, cpu_model, ram_total_bytes, os_caption
FROM ep WHERE ram_total_bytes IS NOT NULL ORDER BY ram_total_bytes DESC LIMIT 15`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["missing_serial"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, eff_model, eff_vendor, collection_method
FROM ep WHERE collected AND eff_serial IS NULL ORDER BY name LIMIT 100`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["duplicate_serials"], err = eiRows(ctx, p, eiModelCTE+`
SELECT eff_serial AS serial, count(*) AS count, array_agg(name ORDER BY name) AS devices
FROM ep WHERE eff_serial IS NOT NULL GROUP BY eff_serial HAVING count(*) > 1 ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 3. Disks ----------------------------------------------------------------

func (s *Server) eiDisks(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	t := eiParseThresholds(r)
	out := map[string]any{"thresholds": t.json()}
	var err error

	if out["risk_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT bucket, count(*) AS count FROM (
  SELECT CASE WHEN min_free IS NULL THEN 'no disk data'
              WHEN min_free < $2::bigint OR (c_free IS NOT NULL AND c_free < $4::bigint) THEN 'critical (< free GB threshold)'
              WHEN min_free_pct IS NOT NULL AND min_free_pct < $3::float8 THEN 'warning (< % threshold)'
              ELSE 'ok' END AS bucket FROM ep
) b GROUP BY bucket ORDER BY count DESC`, loc, t.diskFreeByte, t.diskFreeFrac, t.cFreeBytes); err != nil {
		writeErr(w, err)
		return
	}
	if out["lowest_free"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, disk_count, min_free, c_free, c_total,
       CASE WHEN c_total > 0 THEN round((c_free::numeric/c_total)*100,1) ELSE NULL END AS c_free_pct
FROM ep WHERE min_free IS NOT NULL ORDER BY min_free ASC LIMIT 25`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["near_full_c"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, c_free, c_total,
       CASE WHEN c_total > 0 THEN round((c_free::numeric/c_total)*100,1) ELSE NULL END AS c_free_pct
FROM ep WHERE c_free IS NOT NULL AND (c_free < $2::bigint OR (c_total > 0 AND c_free::float8/c_total < $3::float8))
ORDER BY c_free ASC LIMIT 25`, loc, t.cFreeBytes, t.diskFreeFrac); err != nil {
		writeErr(w, err)
		return
	}
	if out["disk_count_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT disk_count::text AS label, count(*) AS count FROM ep WHERE collected GROUP BY disk_count ORDER BY disk_count`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["avg_free_by_site"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(site_name,'(unassigned)') AS site, count(*) FILTER (WHERE min_free IS NOT NULL) AS devices,
       round(avg(min_free) FILTER (WHERE min_free IS NOT NULL)/1073741824.0, 1) AS avg_min_free_gb
FROM ep GROUP BY site ORDER BY avg_min_free_gb NULLS LAST`, loc); err != nil {
		writeErr(w, err)
		return
	}
	// Drive type/health if available (mostly NULL today — surfaced honestly).
	if out["filesystem_distribution"], err = eiRows(ctx, p, `
SELECT COALESCE(NULLIF(trim(filesystem),''),'unknown') AS label, count(*) AS count
FROM os_disks WHERE ($1::text[] IS NULL OR device_id IN (
  SELECT id FROM devices WHERE deleted_at IS NULL AND location_id::text = ANY($1::text[])))
GROUP BY 1 ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 4. Software -------------------------------------------------------------

func (s *Server) eiSoftware(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query().Get("q")     // free-text filter for the top-software list
	qLike := "%" + q + "%"
	out := map[string]any{}
	var err error

	// scoped software base view used by several queries below
	const swScope = `swx AS (
  SELECT sw.* FROM os_software sw
  WHERE ($1::text[] IS NULL OR sw.device_id IN (
    SELECT id FROM devices WHERE deleted_at IS NULL AND location_id::text = ANY($1::text[])))
)`

	if out["top_installed"], err = eiRows(ctx, p, `WITH `+swScope+`
SELECT name, count(DISTINCT device_id) AS device_count, count(*) AS instances,
       count(DISTINCT version) AS version_count
FROM swx WHERE ($2='' OR name ILIKE $3 OR COALESCE(publisher,'') ILIKE $3)
GROUP BY name ORDER BY device_count DESC, name LIMIT 100`, loc, q, qLike); err != nil {
		writeErr(w, err)
		return
	}
	if out["single_device"], err = eiRows(ctx, p, `WITH `+swScope+`
SELECT name, max(version) AS version, max(publisher) AS publisher
FROM swx GROUP BY name HAVING count(DISTINCT device_id)=1 ORDER BY name LIMIT 200`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["publishers"], err = eiRows(ctx, p, `WITH `+swScope+`
SELECT COALESCE(NULLIF(trim(publisher),''),'unknown') AS label, count(*) AS count,
       count(DISTINCT device_id) AS device_count
FROM swx GROUP BY 1 ORDER BY count DESC LIMIT 40`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["multi_version"], err = eiRows(ctx, p, `WITH `+swScope+`
SELECT name, count(DISTINCT version) AS version_count, count(DISTINCT device_id) AS device_count,
       array_agg(DISTINCT version ORDER BY version) AS versions
FROM swx GROUP BY name HAVING count(DISTINCT version) > 1 ORDER BY version_count DESC, name LIMIT 100`, loc); err != nil {
		writeErr(w, err)
		return
	}
	// Microsoft / Office / browser / remote-tools / AV categories (name+publisher heuristics).
	if out["categories"], err = eiRow(ctx, p, `WITH `+swScope+`, dev AS (SELECT DISTINCT device_id FROM swx)
SELECT
  (SELECT count(*) FROM swx WHERE publisher ILIKE '%microsoft%') AS microsoft_rows,
  (SELECT count(DISTINCT device_id) FROM swx WHERE name ILIKE '%office%' OR name ILIKE '%microsoft 365%') AS office_devices,
  (SELECT count(DISTINCT device_id) FROM swx WHERE name ~* '(chrome|firefox|edge|opera|brave)') AS browser_devices,
  (SELECT count(DISTINCT device_id) FROM swx WHERE name ~* '(teamviewer|anydesk|vnc|remote desktop|logmein|dameware|splashtop|rustdesk)') AS remote_tool_devices,
  (SELECT count(DISTINCT device_id) FROM swx WHERE name ~* '(defender|kaspersky|eset|bitdefender|sophos|symantec|mcafee|trend micro|crowdstrike|sentinelone|webroot|avast|avg|malwarebytes|cortex|carbon black)') AS antivirus_devices,
  (SELECT count(*) FROM dev) AS devices_with_software`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["antivirus"], err = eiRows(ctx, p, `WITH `+swScope+`
SELECT name, count(DISTINCT device_id) AS device_count
FROM swx WHERE name ~* '(defender|kaspersky|eset|bitdefender|sophos|symantec|mcafee|trend micro|crowdstrike|sentinelone|webroot|avast|avg|malwarebytes|cortex|carbon black)'
GROUP BY name ORDER BY device_count DESC LIMIT 30`, loc); err != nil {
		writeErr(w, err)
		return
	}
	// Devices with no software inventory (managed but empty) — honest gap list.
	if out["no_software_devices"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, collection_method, software_note
FROM ep WHERE collected AND sw_count=0 ORDER BY name LIMIT 200`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 5. Processes ------------------------------------------------------------

func (s *Server) eiProcesses(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	var err error
	const prScope = `prx AS (
  SELECT pr.* FROM os_processes pr
  WHERE ($1::text[] IS NULL OR pr.device_id IN (
    SELECT id FROM devices WHERE deleted_at IS NULL AND location_id::text = ANY($1::text[])))
)`

	if out["top_common"], err = eiRows(ctx, p, `WITH `+prScope+`
SELECT name, count(DISTINCT device_id) AS device_count, count(*) AS instances,
       round(avg(mem_bytes)) AS avg_mem_bytes, max(mem_bytes) AS max_mem_bytes
FROM prx GROUP BY name ORDER BY device_count DESC, instances DESC LIMIT 50`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["highest_memory"], err = eiRows(ctx, p, `WITH `+prScope+`
SELECT name, max(mem_bytes) AS max_mem_bytes, count(DISTINCT device_id) AS device_count
FROM prx WHERE mem_bytes IS NOT NULL GROUP BY name ORDER BY max_mem_bytes DESC LIMIT 30`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["unique_to_one"], err = eiRows(ctx, p, `WITH `+prScope+`
SELECT name, count(*) AS instances FROM prx GROUP BY name HAVING count(DISTINCT device_id)=1 ORDER BY name LIMIT 200`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["process_count_per_device"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, proc_count
FROM ep WHERE collected ORDER BY proc_count DESC LIMIT 25`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// eiProcessDevices — GET /endpoint-intelligence/processes/devices?name=...
// Devices running (or, with present=false, NOT running) a named process.
func (s *Server) eiProcessDevices(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query param required", http.StatusBadRequest)
		return
	}
	present := r.URL.Query().Get("present") != "false"
	rows, err := eiRows(r.Context(), p, eiModelCTE+`, hits AS (
  SELECT device_id::text AS did FROM os_processes WHERE lower(name)=lower($2) GROUP BY 1
)
SELECT e.device_id, e.name, e.ip, e.site_name, e.os_caption
FROM ep e
WHERE e.collected AND ($3::bool = (e.device_id IN (SELECT did FROM hits)))
ORDER BY e.name LIMIT 500`, loc, name, present)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"process": name, "present": present, "devices": rows})
}

// ---- 6. Services -------------------------------------------------------------

func (s *Server) eiServices(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	var err error
	const svScope = `svx AS (
  SELECT sv.* FROM os_services sv
  WHERE ($1::text[] IS NULL OR sv.device_id IN (
    SELECT id FROM devices WHERE deleted_at IS NULL AND location_id::text = ANY($1::text[])))
)`

	if out["common_services"], err = eiRows(ctx, p, `WITH `+svScope+`
SELECT name, max(display_name) AS display_name, count(DISTINCT device_id) AS device_count,
       count(*) FILTER (WHERE status IN ('Running','Started','OK')) AS running_count
FROM svx GROUP BY name ORDER BY device_count DESC, name LIMIT 60`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["auto_stopped"], err = eiRows(ctx, p, `WITH `+svScope+`
SELECT name, max(display_name) AS display_name, count(DISTINCT device_id) AS device_count
FROM svx WHERE start_type ILIKE 'Auto%' AND COALESCE(status,'') NOT IN ('Running','Started','OK','Auto')
GROUP BY name ORDER BY device_count DESC, name LIMIT 100`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["state_distribution"], err = eiRows(ctx, p, `WITH `+svScope+`
SELECT COALESCE(NULLIF(trim(status),''),'unknown') AS label, count(*) AS count
FROM svx GROUP BY 1 ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["single_device"], err = eiRows(ctx, p, `WITH `+svScope+`
SELECT name, max(display_name) AS display_name FROM svx GROUP BY name HAVING count(DISTINCT device_id)=1 ORDER BY name LIMIT 200`, loc); err != nil {
		writeErr(w, err)
		return
	}
	// Key service status across the managed fleet (WinRM / RemoteRegistry / Windows
	// Update / WMI+RPC) — running vs stopped vs absent per device, honest counts.
	if out["key_services"], err = eiRows(ctx, p, eiModelCTE+`, mgd AS (SELECT device_id FROM ep WHERE collected)
SELECT k.svc AS service, k.display AS display_name,
  (SELECT count(*) FROM mgd) AS managed_devices,
  count(DISTINCT sv.device_id) FILTER (WHERE sv.status IN ('Running','Started','OK')) AS running,
  count(DISTINCT sv.device_id) FILTER (WHERE sv.status IS NOT NULL AND sv.status NOT IN ('Running','Started','OK')) AS stopped
FROM (VALUES ('WinRM','Windows Remote Management'),('RemoteRegistry','Remote Registry'),
             ('wuauserv','Windows Update'),('Winmgmt','WMI'),('RpcSs','RPC')) AS k(svc,display)
LEFT JOIN os_services sv ON lower(sv.name)=lower(k.svc) AND sv.device_id::text IN (SELECT device_id FROM mgd)
GROUP BY k.svc, k.display ORDER BY k.svc`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 7. OS -------------------------------------------------------------------

func (s *Server) eiOS(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	var err error

	if out["caption_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(NULLIF(trim(os_caption),''),'unknown') AS label, count(*) AS count
FROM ep GROUP BY 1 ORDER BY count DESC, label LIMIT 40`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["family_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT label, count(*) AS count FROM (
  SELECT CASE
    WHEN os_caption ~* 'Windows 11' THEN 'Windows 11'
    WHEN os_caption ~* 'Windows 10' THEN 'Windows 10'
    WHEN os_caption ~* 'Windows 8' THEN 'Windows 8'
    WHEN os_caption ~* 'Windows 7' THEN 'Windows 7'
    WHEN os_caption ~* 'Windows XP|Vista' THEN 'Windows XP/Vista'
    WHEN os_caption ~* 'Server 2022' THEN 'Server 2022'
    WHEN os_caption ~* 'Server 2019' THEN 'Server 2019'
    WHEN os_caption ~* 'Server 2016' THEN 'Server 2016'
    WHEN os_caption ~* 'Server 201[23]' THEN 'Server 2012/2012R2'
    WHEN os_caption ~* 'Server 200[0-9]' THEN 'Server 2008 or older'
    WHEN os_caption ~* 'Server' THEN 'Windows Server (other)'
    WHEN os_family='linux' OR os_caption ~* 'linux|ubuntu|debian|centos|red hat|rhel' THEN 'Linux'
    WHEN os_caption IS NULL OR trim(os_caption)='' THEN 'unknown'
    ELSE 'other' END AS label FROM ep
) x GROUP BY label ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["old_os_devices"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, os_caption, os_build, collection_method
FROM ep WHERE os_caption ~* '(Windows 7|Windows XP|Windows Vista|Windows 8|Server 2003|Server 2008|Server 2012)'
ORDER BY os_caption, name LIMIT 200`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["build_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(NULLIF(trim(os_build),''),'unknown') AS label, count(*) AS count
FROM ep WHERE collected GROUP BY 1 ORDER BY count DESC LIMIT 40`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["domain_workgroup"], err = eiRows(ctx, p, eiModelCTE+`
SELECT label, count(*) AS count FROM (
  SELECT CASE WHEN domain <> '' THEN 'domain: '||domain
              WHEN workgroup <> '' THEN 'workgroup: '||workgroup
              ELSE 'unknown' END AS label FROM ep WHERE collected
) x GROUP BY label ORDER BY count DESC LIMIT 40`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["collection_method_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT CASE collection_method
         WHEN 'winrm' THEN 'direct WinRM'
         WHEN 'winrm-agent' THEN 'relay agent (WMI/CIM)'
         WHEN 'wmi' THEN 'relay agent WMI/DCOM'
         WHEN 'winrm-native' THEN 'native WinRM'
         WHEN 'ssh' THEN 'SSH'
         WHEN 'snmp' THEN 'SNMP'
         WHEN 'manual' THEN 'manual'
         WHEN NULL THEN 'not collected'
         ELSE COALESCE(collection_method,'not collected') END AS label,
       count(*) AS count
FROM ep GROUP BY collection_method ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["not_rebooted"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, last_boot, uptime_seconds, os_caption
FROM ep WHERE last_boot IS NOT NULL ORDER BY last_boot ASC LIMIT 25`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["missing_os"], err = eiRow(ctx, p, eiModelCTE+`
SELECT count(*) FILTER (WHERE NOT collected) AS missing_os_inventory,
       count(*) FILTER (WHERE collected AND (os_caption IS NULL OR trim(os_caption)='')) AS collected_without_caption
FROM ep`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 8. Network --------------------------------------------------------------

func (s *Server) eiNetwork(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	var err error
	const nicScope = `nicx AS (
  SELECT nc.* FROM os_nics nc
  WHERE ($1::text[] IS NULL OR nc.device_id IN (
    SELECT id FROM devices WHERE deleted_at IS NULL AND location_id::text = ANY($1::text[])))
)`

	if out["multi_nic"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, nic_count
FROM ep WHERE nic_count > 1 ORDER BY nic_count DESC, name LIMIT 50`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["no_nic_devices"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, collection_method
FROM ep WHERE collected AND nic_count=0 ORDER BY name LIMIT 100`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["apipa"], err = eiRows(ctx, p, `WITH `+nicScope+`
SELECT device_id::text AS device_id, name AS nic, ip_addresses
FROM nicx WHERE ip_addresses LIKE '169.254.%' OR ip_addresses LIKE '%,169.254.%' ORDER BY 1 LIMIT 100`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["duplicate_mac"], err = eiRows(ctx, p, `WITH `+nicScope+`
SELECT lower(mac) AS mac, count(DISTINCT device_id) AS device_count
FROM nicx WHERE mac IS NOT NULL AND trim(mac) <> '' GROUP BY 1 HAVING count(DISTINCT device_id) > 1 ORDER BY device_count DESC LIMIT 100`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["link_speed_distribution"], err = eiRows(ctx, p, `WITH `+nicScope+`
SELECT COALESCE(link_speed_mbps::text,'unknown') AS label, count(*) AS count
FROM nicx GROUP BY link_speed_mbps ORDER BY link_speed_mbps NULLS LAST`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["site_summary"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(site_name,'(unassigned)') AS site, count(*) AS devices,
       count(*) FILTER (WHERE collected) AS managed,
       count(*) FILTER (WHERE NOT collected) AS unmanaged
FROM ep GROUP BY site ORDER BY devices DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 9. Collection health ----------------------------------------------------

func (s *Server) eiCollectionHealth(w http.ResponseWriter, r *http.Request) {
	p, loc, ok := s.eiGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	var err error

	if out["method_distribution"], err = eiRows(ctx, p, eiModelCTE+`
SELECT COALESCE(collection_method,'not collected') AS label, count(*) AS count
FROM ep GROUP BY collection_method ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["staleness"], err = eiRow(ctx, p, eiModelCTE+`
SELECT count(*) FILTER (WHERE collected AND collected_at >= now()-interval '24 hours') AS within_24h,
       count(*) FILTER (WHERE collected AND collected_at < now()-interval '24 hours' AND collected_at >= now()-interval '7 days') AS within_7d,
       count(*) FILTER (WHERE collected AND collected_at < now()-interval '7 days' AND collected_at >= now()-interval '30 days') AS within_30d,
       count(*) FILTER (WHERE collected AND collected_at < now()-interval '30 days') AS older_30d,
       count(*) FILTER (WHERE NOT collected) AS never
FROM ep`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["partial_inventory"], err = eiRow(ctx, p, eiModelCTE+`
SELECT count(*) FILTER (WHERE collected AND sw_count=0) AS missing_software,
       count(*) FILTER (WHERE collected AND proc_count=0) AS missing_processes,
       count(*) FILTER (WHERE collected AND disk_count=0) AS missing_disks,
       count(*) FILTER (WHERE collected AND svc_count=0) AS missing_services,
       count(*) FILTER (WHERE collected AND nic_count=0) AS missing_nics,
       count(*) FILTER (WHERE collected AND (sw_count=0 OR proc_count=0 OR disk_count=0 OR svc_count=0 OR nic_count=0)) AS any_partial
FROM ep`, loc); err != nil {
		writeErr(w, err)
		return
	}
	// Reason breakdown from software_note + derived signals (host offline, stale).
	if out["reason_breakdown"], err = eiRows(ctx, p, eiModelCTE+`
SELECT reason, count(*) AS count FROM (
  SELECT CASE
    WHEN NOT collected AND status='down' THEN 'host offline'
    WHEN NOT collected THEN 'never collected / no credential'
    WHEN software_note ~* 'access_denied|denied' THEN 'access denied'
    WHEN software_note ~* 'remote_registry_disabled|disabled' THEN 'remote registry / WinRM disabled'
    WHEN software_note ~* 'rpc_unreachable|unreachable|network path' THEN 'WMI/RPC unavailable'
    WHEN software_note ~* 'registry_access_denied|no_software_method' THEN 'software registry unavailable'
    WHEN software_note ~* 'service_start_failed' THEN 'service start failed'
    WHEN software_note ~* 'timeout' THEN 'timeout'
    WHEN software_note ~* 'unsupported' THEN 'unsupported method'
    WHEN collected AND sw_count=0 THEN 'software missing (reason not recorded)'
    ELSE 'ok' END AS reason FROM ep
) x GROUP BY reason ORDER BY count DESC`, loc); err != nil {
		writeErr(w, err)
		return
	}
	if out["stale_devices"], err = eiRows(ctx, p, eiModelCTE+`
SELECT device_id, name, ip, site_name, collection_method, collected_at, software_note
FROM ep WHERE collected AND collected_at < now()-interval '7 days' ORDER BY collected_at ASC LIMIT 100`, loc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
