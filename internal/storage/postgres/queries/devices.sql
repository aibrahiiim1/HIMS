-- name: GetDevice :one
SELECT * FROM devices WHERE id = $1 AND deleted_at IS NULL;

-- name: ListDevicesByCategory :many
SELECT * FROM devices
WHERE category = $1 AND deleted_at IS NULL
ORDER BY name;

-- name: LiveDeviceByIPAndLocation :one
-- Identity reconciliation key (multi-hotel safe): same IP can recur across
-- hotels, so a live device is unique by (primary_ip, location).
SELECT * FROM devices
WHERE primary_ip = $1 AND location_id = $2 AND deleted_at IS NULL;

-- name: CreateDevice :one
INSERT INTO devices (
    location_id, primary_ip, hostname, name, vendor, model, serial,
    os_version, category, status, driver, credential_id, metadata
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
RETURNING *;

-- name: UpdateDiscoveredDevice :one
-- Reconcile path: refresh a live device's mutable identity fields on
-- re-discovery (keyed by the caller to the (primary_ip, location) match).
UPDATE devices SET
    hostname = $2, name = $3, vendor = $4, model = $5, serial = $6,
    os_version = $7, category = $8, driver = $9, status = $10,
    last_discovery_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetDeviceCredential :exec
-- Bind-on-success: record the credential that last authenticated.
UPDATE devices SET credential_id = $2, updated_at = now() WHERE id = $1;

-- name: TouchDeviceDiscovery :exec
UPDATE devices SET last_discovery_at = $2, updated_at = now() WHERE id = $1;

-- name: UpsertDeviceFact :exec
INSERT INTO device_facts (device_id, key, value, value_json, driver, observed_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (device_id, key) DO UPDATE SET
    value = EXCLUDED.value,
    value_json = EXCLUDED.value_json,
    driver = EXCLUDED.driver,
    observed_at = now();

-- name: ListDeviceFacts :many
SELECT * FROM device_facts WHERE device_id = $1 ORDER BY key;

-- name: AddDeviceRole :exec
INSERT INTO device_roles (device_id, role, source)
VALUES ($1, $2, $3)
ON CONFLICT (device_id, role) DO NOTHING;

-- name: ListDeviceRoles :many
SELECT * FROM device_roles WHERE device_id = $1 ORDER BY role;

-- name: RoleSummary :many
-- Fleet-wide role rollup: how many devices hold each role (the CMDB role cut).
SELECT role, COUNT(*) AS count
FROM device_roles
GROUP BY role
ORDER BY count DESC, role;

-- name: ListDevicesByRole :many
SELECT d.* FROM devices d
JOIN device_roles r ON r.device_id = d.id
WHERE r.role = $1
ORDER BY d.name;
