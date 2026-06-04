-- name: InsertConfigBackup :one
INSERT INTO config_backups (device_id, captured_by, source, driver, command, content_encrypted, key_id, sha256, size_bytes, changed)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
RETURNING id, device_id, captured_at, captured_by, source, driver, command, key_id, sha256, size_bytes, changed;

-- name: ListConfigBackupsByDevice :many
-- Metadata only (no content_encrypted) — newest first.
SELECT id, device_id, captured_at, captured_by, source, driver, command, key_id, sha256, size_bytes, changed
FROM config_backups
WHERE device_id = $1
ORDER BY captured_at DESC
LIMIT $2;

-- name: GetLatestConfigBackup :one
-- Drift lookup: the most recent backup's hash for this device.
SELECT id, device_id, captured_at, sha256
FROM config_backups
WHERE device_id = $1
ORDER BY captured_at DESC
LIMIT 1;

-- name: GetConfigBackupContent :one
-- Full row including the encrypted blob — used only by the key-gated
-- content/diff endpoints, never by list responses.
SELECT id, device_id, captured_at, captured_by, source, driver, command, content_encrypted, key_id, sha256, size_bytes
FROM config_backups
WHERE id = $1;

-- name: ListRecentConfigBackups :many
-- Fleet activity feed for the Config page: recent captures with device name.
SELECT cb.id, cb.device_id, d.name AS device_name, cb.captured_at, cb.captured_by,
       cb.source, cb.driver, cb.sha256, cb.size_bytes, cb.changed
FROM config_backups cb
JOIN devices d ON d.id = cb.device_id
ORDER BY cb.captured_at DESC
LIMIT $1;

-- name: CountConfigBackupStats :one
-- Overview KPIs: total versions, distinct devices backed up, and changes today.
SELECT
    COUNT(*)::bigint AS total_backups,
    COUNT(DISTINCT device_id)::bigint AS devices_backed_up,
    COUNT(*) FILTER (WHERE changed AND captured_at >= now() - interval '24 hours')::bigint AS changed_today
FROM config_backups;
