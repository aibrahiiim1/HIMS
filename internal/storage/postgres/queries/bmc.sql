-- name: UpsertBMCInfo :exec
INSERT INTO bmc_info (device_id, vendor, controller_kind, model, serial, firmware_version, power_state, health,
    cpu_model, cpu_count, cpu_cores, memory_gib, bios_version, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (device_id) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    controller_kind = EXCLUDED.controller_kind,
    model = EXCLUDED.model,
    serial = EXCLUDED.serial,
    firmware_version = EXCLUDED.firmware_version,
    power_state = EXCLUDED.power_state,
    health = EXCLUDED.health,
    cpu_model = EXCLUDED.cpu_model,
    cpu_count = EXCLUDED.cpu_count,
    cpu_cores = EXCLUDED.cpu_cores,
    memory_gib = EXCLUDED.memory_gib,
    bios_version = EXCLUDED.bios_version,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: UpsertBMCComponent :exec
-- One detailed Redfish hardware item (cpu | memory | controller | volume | drive).
INSERT INTO bmc_components (device_id, kind, name, model, serial, status, capacity_bytes, detail, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (device_id, kind, name) DO UPDATE SET
    model = EXCLUDED.model,
    serial = EXCLUDED.serial,
    status = EXCLUDED.status,
    capacity_bytes = EXCLUDED.capacity_bytes,
    detail = EXCLUDED.detail,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListBMCComponents :many
SELECT * FROM bmc_components WHERE device_id = $1
ORDER BY CASE kind WHEN 'cpu' THEN 1 WHEN 'memory' THEN 2 WHEN 'controller' THEN 3 WHEN 'volume' THEN 4 WHEN 'drive' THEN 5 ELSE 6 END, name;

-- name: DeleteStaleBMCComponents :exec
DELETE FROM bmc_components
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- name: GetBMCInfo :one
SELECT * FROM bmc_info WHERE device_id = $1;

-- name: UpsertBMCSensor :exec
INSERT INTO bmc_sensors (device_id, kind, name, status, reading, unit, has_reading, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (device_id, kind, name) DO UPDATE SET
    status = EXCLUDED.status,
    reading = EXCLUDED.reading,
    unit = EXCLUDED.unit,
    has_reading = EXCLUDED.has_reading,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListBMCSensors :many
SELECT * FROM bmc_sensors WHERE device_id = $1 ORDER BY kind, name;

-- name: DeleteStaleBMCSensors :exec
DELETE FROM bmc_sensors
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;
