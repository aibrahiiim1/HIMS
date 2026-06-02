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
SELECT * FROM monitoring_checks ORDER BY created_at DESC LIMIT 500;

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
-- Live fleet rollup: how many checks sit in each status bucket.
SELECT last_status AS status, COUNT(*) AS count
FROM monitoring_checks
WHERE enabled
GROUP BY last_status;

-- name: ListDevicesNeedingDefaultCheck :many
-- Devices with a reachable IP but no monitoring check yet — the seeder turns
-- each into a default TCP check (port chosen by category).
SELECT id, primary_ip, category FROM devices
WHERE primary_ip IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM monitoring_checks c WHERE c.device_id = devices.id)
LIMIT 1000;

-- name: UpdateDeviceMonitoringStatus :exec
-- Reflect the worst current check status onto the device row so device lists
-- show a live health badge without a per-row sample query.
UPDATE devices SET
    status = $2,
    last_monitoring_at = now(),
    updated_at = now()
WHERE id = $1;
