-- name: UpsertMonitoringCheck :one
-- Idempotent registration: re-registering the same (device, kind, port)
-- updates the schedule knobs without resetting the live status counters.
INSERT INTO monitoring_checks (device_id, kind, target_port, oid, interval_seconds, down_threshold, enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (device_id, kind, target_port) DO UPDATE SET
    oid = EXCLUDED.oid,
    interval_seconds = EXCLUDED.interval_seconds,
    down_threshold = EXCLUDED.down_threshold,
    enabled = EXCLUDED.enabled,
    updated_at = now()
RETURNING *;

-- name: GetMonitoringCheck :one
SELECT * FROM monitoring_checks WHERE id = $1;

-- name: ListMonitoringChecks :many
-- Global checks list (Health Overview). Joined to the device so the UI can
-- identify each check by device name / IP (not just a bare port), and ordered
-- by device then reachability-first so a device's checks group together.
SELECT c.*,
       d.name AS device_name,
       d.primary_ip AS device_ip,
       d.category AS device_category
FROM monitoring_checks c
JOIN devices d ON d.id = c.device_id AND d.deleted_at IS NULL
ORDER BY d.name, (c.role <> 'reachability'), c.kind, c.target_port
LIMIT 500;

-- name: SetMonitoringCheckRole :exec
-- Mark a check as reachability (drives device status) or supplemental (extra,
-- informational only). Used when an operator adds a check beyond the default.
UPDATE monitoring_checks SET role = $2, updated_at = now() WHERE id = $1;

-- name: ListMonitoringChecksByDevice :many
SELECT * FROM monitoring_checks WHERE device_id = $1 ORDER BY kind, target_port;

-- name: ListDueMonitoringChecks :many
-- A check is due when enabled and either never run or its interval elapsed.
SELECT * FROM monitoring_checks
WHERE enabled
  AND (last_run_at IS NULL
       OR last_run_at + make_interval(secs => interval_seconds) <= now())
ORDER BY last_run_at NULLS FIRST
LIMIT 500;

-- name: SetMonitoringCheckEnabled :one
UPDATE monitoring_checks SET enabled = $2, updated_at = now()
WHERE id = $1 RETURNING *;

-- name: DeleteMonitoringCheck :exec
DELETE FROM monitoring_checks WHERE id = $1;

-- name: RecordMonitoringResult :one
-- Persist the rollup the engine computed (status + failure counter) onto the
-- check after a poll. History rows go to monitoring_samples separately.
UPDATE monitoring_checks SET
    last_run_at = now(),
    last_status = $2,
    last_latency_ms = $3,
    consecutive_failures = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: InsertMonitoringSample :exec
INSERT INTO monitoring_samples (check_id, device_id, status, latency_ms, value_num, error)
VALUES ($1,$2,$3,$4,$5,$6);

-- name: ListMonitoringSamplesByDevice :many
SELECT * FROM monitoring_samples
WHERE device_id = $1
ORDER BY time DESC
LIMIT $2;

-- name: ListMonitoringSamplesByCheck :many
SELECT * FROM monitoring_samples
WHERE check_id = $1
ORDER BY time DESC
LIMIT $2;

-- name: MonitoringStatusOverview :many
-- Fleet health rollup, DEVICE-based: how many MONITORED devices sit in each
-- reachability bucket. We count devices (not checks) and map the device's
-- rolled-up status onto the up/down/warning/unknown vocabulary, so the KPI
-- counts match the inventory reachability filter exactly (a click lands on the
-- same devices). A failing SUPPLEMENTAL check surfaces here as "warning" (the
-- rollup degrades the device to warning, never down) — it lowers the health
-- score and is clickable, but never inflates the "down"/offline bucket. A device
-- is "monitored" when it has at least one enabled check.
SELECT
    (CASE d.status
        WHEN 'up' THEN 'up'
        WHEN 'down' THEN 'down'
        WHEN 'warning' THEN 'warning'
        WHEN 'needs_attention' THEN 'warning'
        ELSE 'unknown'
     END)::text AS status,
    COUNT(*)::bigint AS count
FROM devices d
WHERE d.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM monitoring_checks c WHERE c.device_id = d.id AND c.enabled)
GROUP BY 1;

-- name: ListDevicesNeedingDefaultCheck :many
-- Devices with a reachable IP but no monitoring check yet — the seeder turns
-- each into a default TCP check (port chosen by category + os_family).
SELECT id, primary_ip, category, os_family FROM devices
WHERE primary_ip IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM monitoring_checks c WHERE c.device_id = devices.id)
LIMIT 1000;

-- name: DeleteDeviceReachabilityChecks :exec
-- Remove a device's TCP reachability checks so a fresh, evidence-based one can
-- replace them (used when a scan re-points the check at a port the host
-- actually answered on). SNMP/metric checks are left untouched.
DELETE FROM monitoring_checks WHERE device_id = $1 AND kind = 'tcp';

-- name: UpdateDeviceMonitoringStatus :exec
-- Reflect the worst current check status onto the device row so device lists
-- show a live health badge without a per-row sample query.
UPDATE devices SET
    status = $2,
    last_monitoring_at = now(),
    updated_at = now()
WHERE id = $1;
