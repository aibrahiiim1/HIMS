-- name: UpsertBMCInfo :exec
INSERT INTO bmc_info (device_id, vendor, controller_kind, model, serial, firmware_version, power_state, health, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (device_id) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    controller_kind = EXCLUDED.controller_kind,
    model = EXCLUDED.model,
    serial = EXCLUDED.serial,
    firmware_version = EXCLUDED.firmware_version,
    power_state = EXCLUDED.power_state,
    health = EXCLUDED.health,
    last_seen_at = EXCLUDED.last_seen_at;

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
