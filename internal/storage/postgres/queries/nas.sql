-- name: UpsertNASInfo :exec
INSERT INTO nas_info (device_id, vendor, model, firmware, serial, hostname, cpu_pct,
    mem_total_bytes, mem_used_bytes, cpu_temp_c, sys_temp_c, uptime_seconds,
    disk_count, volume_count, health, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (device_id) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    model = EXCLUDED.model,
    firmware = EXCLUDED.firmware,
    serial = EXCLUDED.serial,
    hostname = EXCLUDED.hostname,
    cpu_pct = EXCLUDED.cpu_pct,
    mem_total_bytes = EXCLUDED.mem_total_bytes,
    mem_used_bytes = EXCLUDED.mem_used_bytes,
    cpu_temp_c = EXCLUDED.cpu_temp_c,
    sys_temp_c = EXCLUDED.sys_temp_c,
    uptime_seconds = EXCLUDED.uptime_seconds,
    disk_count = EXCLUDED.disk_count,
    volume_count = EXCLUDED.volume_count,
    health = EXCLUDED.health,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: GetNASInfo :one
SELECT * FROM nas_info WHERE device_id = $1;

-- name: UpsertNASDisk :exec
INSERT INTO nas_disks (device_id, slot, vendor, model, serial, interface_type,
    capacity_bytes, temp_c, health, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (device_id, slot) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    model = EXCLUDED.model,
    serial = EXCLUDED.serial,
    interface_type = EXCLUDED.interface_type,
    capacity_bytes = EXCLUDED.capacity_bytes,
    temp_c = EXCLUDED.temp_c,
    health = EXCLUDED.health,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListNASDisks :many
SELECT * FROM nas_disks WHERE device_id = $1 ORDER BY slot;

-- name: DeleteStaleNASDisks :exec
DELETE FROM nas_disks WHERE device_id = $1 AND last_seen_at < $2;

-- name: UpsertNASVolume :exec
INSERT INTO nas_volumes (device_id, idx, name, fs_type, total_bytes, used_bytes, status, pool, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (device_id, idx) DO UPDATE SET
    name = EXCLUDED.name,
    fs_type = EXCLUDED.fs_type,
    total_bytes = EXCLUDED.total_bytes,
    used_bytes = EXCLUDED.used_bytes,
    status = EXCLUDED.status,
    pool = EXCLUDED.pool,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListNASVolumes :many
SELECT * FROM nas_volumes WHERE device_id = $1 ORDER BY total_bytes DESC, idx;

-- name: DeleteStaleNASVolumes :exec
DELETE FROM nas_volumes WHERE device_id = $1 AND last_seen_at < $2;

-- name: UpsertNASISCSI :exec
INSERT INTO nas_iscsi (device_id, kind, idx, name, capacity_bytes, status, iqn, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (device_id, kind, idx) DO UPDATE SET
    name = EXCLUDED.name,
    capacity_bytes = EXCLUDED.capacity_bytes,
    status = EXCLUDED.status,
    iqn = EXCLUDED.iqn,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListNASISCSI :many
SELECT * FROM nas_iscsi WHERE device_id = $1 ORDER BY kind, idx;

-- name: DeleteStaleNASISCSI :exec
DELETE FROM nas_iscsi WHERE device_id = $1 AND last_seen_at < $2;

-- name: UpsertNASPool :exec
INSERT INTO nas_pools (device_id, idx, name, raid_type, raw_bytes, status, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (device_id, idx) DO UPDATE SET
    name = EXCLUDED.name,
    raid_type = EXCLUDED.raid_type,
    raw_bytes = EXCLUDED.raw_bytes,
    status = EXCLUDED.status,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListNASPools :many
SELECT * FROM nas_pools WHERE device_id = $1 ORDER BY idx;

-- name: DeleteStaleNASPools :exec
DELETE FROM nas_pools WHERE device_id = $1 AND last_seen_at < $2;

-- name: UpsertNASFan :exec
INSERT INTO nas_fans (device_id, idx, name, rpm, last_seen_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (device_id, idx) DO UPDATE SET
    name = EXCLUDED.name,
    rpm = EXCLUDED.rpm,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListNASFans :many
SELECT * FROM nas_fans WHERE device_id = $1 ORDER BY idx;

-- name: DeleteStaleNASFans :exec
DELETE FROM nas_fans WHERE device_id = $1 AND last_seen_at < $2;
