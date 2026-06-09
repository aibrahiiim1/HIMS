-- Analytics aggregations powering the enterprise dashboards (Dashboard, NOC
-- Wallboard, Health Overview). All grounded in real collected data:
-- monitoring_samples (reachability time-series) and alerts. Bucket granularity
-- and window are passed as text params so one query serves 24h/7d/30d.
--
-- Percentages are intentionally NOT computed in SQL (kept as raw up/total int
-- counts) so the result types stay clean float8/bigint; the handler derives %.
-- Latency / MTTA / MTTR are nullable: avg() over no rows is NULL, which must
-- read as "no data" (not a fake 0) — hence bare aggregates, no ::float8 cast.

-- name: FleetAvailabilitySeries :many
-- Fleet-wide reachability over time: one row per time bucket with up/down/warning
-- sample counts and latency stats. $1 = date_trunc granularity ('hour'|'day'),
-- $2 = window (e.g. '24 hours', '7 days'). Powers the availability trend chart.
SELECT date_trunc($1::text, time)::timestamptz AS bucket,
       count(*)::bigint AS total,
       count(*) FILTER (WHERE status = 'up')::bigint AS up,
       count(*) FILTER (WHERE status = 'down')::bigint AS down,
       count(*) FILTER (WHERE status IN ('warning', 'needs_attention'))::bigint AS warning,
       COALESCE(avg(latency_ms) FILTER (WHERE latency_ms IS NOT NULL), 0)::float8 AS avg_latency_ms,
       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)::float8 AS p95_latency_ms
FROM monitoring_samples
WHERE time > now() - ($2::text)::interval
GROUP BY 1
ORDER BY 1;

-- name: FleetAvailabilitySummary :one
-- Single-row rollup over the same window: sample counts (for uptime %), distinct
-- devices, and latency stats. Used for the headline availability KPI / SLA figure.
SELECT count(*)::bigint AS total,
       count(*) FILTER (WHERE status = 'up')::bigint AS up,
       count(*) FILTER (WHERE status = 'down')::bigint AS down,
       count(*) FILTER (WHERE status IN ('warning', 'needs_attention'))::bigint AS warning,
       count(DISTINCT device_id)::bigint AS devices,
       COALESCE(avg(latency_ms) FILTER (WHERE latency_ms IS NOT NULL), 0)::float8 AS avg_latency_ms,
       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)::float8 AS p95_latency_ms
FROM monitoring_samples
WHERE time > now() - ($1::text)::interval;

-- name: DeviceUptimeRanking :many
-- Per-device availability over the window: sample/up counts (for uptime %),
-- latency, and flap count (status transitions). Ordered worst-first so the UI can
-- show "worst performers" and a flapping list. $1 = window (e.g. '24 hours').
WITH win AS (
    SELECT device_id, status, latency_ms,
           lag(status) OVER (PARTITION BY device_id ORDER BY time) AS prev_status
    FROM monitoring_samples
    WHERE time > now() - ($1::text)::interval
)
SELECT w.device_id,
       d.name,
       d.primary_ip,
       d.category,
       count(*)::bigint AS samples,
       count(*) FILTER (WHERE w.status = 'up')::bigint AS up,
       COALESCE(avg(w.latency_ms) FILTER (WHERE w.latency_ms IS NOT NULL), 0)::float8 AS avg_latency_ms,
       COALESCE(max(w.latency_ms), 0)::float8 AS max_latency_ms,
       count(*) FILTER (WHERE w.prev_status IS NOT NULL AND w.status <> w.prev_status)::bigint AS flaps
FROM win w
JOIN devices d ON d.id = w.device_id AND d.deleted_at IS NULL
GROUP BY w.device_id, d.name, d.primary_ip, d.category
ORDER BY (count(*) FILTER (WHERE w.status = 'up'))::float8 / NULLIF(count(*), 0) ASC NULLS LAST,
         flaps DESC, samples DESC;

-- name: AlertAnalyticsSummary :one
-- Alert volume + responsiveness over the window. MTTA/MTTR are NULL until alerts
-- with acknowledged/resolved timestamps exist (honest empty state). $1 = window.
SELECT count(*) FILTER (WHERE opened_at > now() - ($1::text)::interval)::bigint AS opened,
       count(*) FILTER (WHERE resolved_at > now() - ($1::text)::interval)::bigint AS resolved,
       count(*) FILTER (WHERE status = 'open')::bigint AS open_now,
       count(*) FILTER (WHERE status = 'open' AND severity = 'critical')::bigint AS open_critical,
       COALESCE(avg(EXTRACT(epoch FROM (acknowledged_at - opened_at))::float8) FILTER (WHERE acknowledged_at IS NOT NULL), 0)::float8 AS mtta_seconds,
       COALESCE(avg(EXTRACT(epoch FROM (resolved_at - opened_at))::float8) FILTER (WHERE resolved_at IS NOT NULL), 0)::float8 AS mttr_seconds
FROM alerts;

-- name: AlertOpenedSeries :many
-- Alerts opened per time bucket, split by severity, for the alert-rate trend.
-- $1 = granularity, $2 = window.
SELECT date_trunc($1::text, opened_at)::timestamptz AS bucket,
       count(*)::bigint AS opened,
       count(*) FILTER (WHERE severity = 'critical')::bigint AS critical,
       count(*) FILTER (WHERE severity = 'warning')::bigint AS warning
FROM alerts
WHERE opened_at > now() - ($2::text)::interval
GROUP BY 1
ORDER BY 1;
