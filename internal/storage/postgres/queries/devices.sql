-- name: GetDevice :one
SELECT * FROM devices WHERE id = $1 AND deleted_at IS NULL;

-- name: ListDevicesByCategory :many
SELECT * FROM devices
WHERE category = $1 AND deleted_at IS NULL
ORDER BY name;

-- name: ListAllDevices :many
-- Every live device (the Inventory page), ordered for grouped display.
SELECT * FROM devices
WHERE deleted_at IS NULL
ORDER BY category, name;

-- name: LiveDeviceByIP :one
-- Reconcile key for an UNSCOPED scan (no site selected): match by primary_ip
-- alone so a re-scan updates the existing device regardless of an
-- operator-assigned location_id, instead of duplicating it. Most-recent wins.
SELECT * FROM devices
WHERE primary_ip = $1 AND deleted_at IS NULL
ORDER BY updated_at DESC
LIMIT 1;

-- name: LiveDeviceByIPAndLocation :one
-- Identity reconciliation key (multi-hotel safe): same IP can recur across
-- hotels, so a live device is unique by (primary_ip, location). location_id is
-- matched NULL-safe (IS NOT DISTINCT FROM) so an unscoped scan (no site
-- selected, location_id = NULL) still reconciles instead of duplicating.
SELECT * FROM devices
WHERE primary_ip = $1 AND location_id IS NOT DISTINCT FROM $2 AND deleted_at IS NULL;

-- name: CreateDevice :one
INSERT INTO devices (
    location_id, primary_ip, hostname, name, vendor, model, serial,
    os_version, category, status, driver, credential_id, metadata,
    vlan, device_class, location
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
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

-- name: UpdateDevice :one
-- Operator edit of a device's identity + management attributes (Edit Device).
-- classification_locked, when set, also protects identity (category/vendor/model/
-- serial) from being overwritten by future discovery scans (see the reconcile
-- path UpdateDiscoveredDeviceRespectingLock).
UPDATE devices SET
    name = $2, category = $3, vendor = $4, model = $5, serial = $6,
    os_version = $7, hostname = $8, vlan = $9, device_class = $10,
    location = $11, location_id = $12,
    subtype = $13, notes = $14, criticality = $15, monitoring_enabled = $16,
    classification_locked = $17, manual_classification_reason = $18,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateDeviceHardwareInfo :exec
-- Fill the device's identity/hardware fields from an authenticated deep-OS
-- collection (manufacturer/model/serial/OS caption/hostname). COALESCE(NULLIF…)
-- means a blank incoming value never wipes an existing one — collection only
-- ENRICHES the row, so the Inventory list columns (Vendor/Model/OS) populate
-- without clobbering anything a prior SNMP probe or operator edit set.
UPDATE devices SET
    vendor = COALESCE(NULLIF(sqlc.arg('vendor')::text, ''), vendor),
    model = COALESCE(NULLIF(sqlc.arg('model')::text, ''), model),
    serial = COALESCE(NULLIF(sqlc.arg('serial')::text, ''), serial),
    os_version = COALESCE(NULLIF(sqlc.arg('os_version')::text, ''), os_version),
    hostname = COALESCE(NULLIF(sqlc.arg('hostname')::text, ''), hostname),
    updated_at = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: BulkAssignClassification :execrows
-- Assign vlan/device_class/location to many devices at once (multi-select).
-- Only the provided (non-null) fields are changed — COALESCE keeps the rest —
-- so an operator can set just location, just class, just vlan, or any combo.
UPDATE devices SET
    vlan = COALESCE(sqlc.narg('vlan'), vlan),
    device_class = COALESCE(sqlc.narg('device_class'), device_class),
    location_id = COALESCE(sqlc.narg('location_id'), location_id),
    updated_at = now()
WHERE id = ANY(sqlc.arg('ids')::uuid[]) AND deleted_at IS NULL;

-- name: DeleteDevice :exec
-- Hard delete (cascades to inventory child rows via FK ON DELETE CASCADE).
DELETE FROM devices WHERE id = $1;

-- name: DeleteDevices :execrows
-- Bulk hard delete (multi-select). Returns the number of rows removed.
DELETE FROM devices WHERE id = ANY($1::uuid[]);

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

-- name: ListSNMPIdentityFacts :many
-- Bulk fetch of the raw SNMP system-group identity facts across ALL devices, for
-- Data Quality checks that re-evaluate fingerprints against stored evidence
-- without re-probing. Only the identity keys, not the full fact set.
SELECT device_id, key, value FROM device_facts
WHERE key IN ('snmp.sysobjectid','snmp.sysdescr','snmp.sysname') AND value IS NOT NULL;

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

-- name: UpdateDeviceClassification :one
-- Auto-classification write: set category + OS family + subtype + confidence +
-- evidence trail in one shot. The `classification_locked = false` guard makes
-- this an atomic no-op on operator-overridden devices (0 rows affected →
-- pgx.ErrNoRows), so a manual classification is never silently overwritten.
UPDATE devices SET
    category = $2,
    os_family = $3,
    device_class = $4,
    confidence_score = $5,
    classification_evidence = $6,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND classification_locked = false
RETURNING *;

-- name: SetClassificationLock :one
-- Operator manual override: lock (true) freezes auto-classification for this
-- device; unlock (false) lets the next discovery re-classify it.
UPDATE devices SET
    classification_locked = $2,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ListDevicesByOSFamily :many
SELECT * FROM devices
WHERE os_family = $1 AND deleted_at IS NULL
ORDER BY category, name;

-- name: MarkDeviceVirtual :exec
-- Flag (or unflag) a device as a manually-entered virtual placeholder.
UPDATE devices SET is_virtual = $2, updated_at = now() WHERE id = $1;

-- name: CountVirtualDevices :one
-- Headline count for the "N devices, M virtual" indicator.
SELECT COUNT(*)::bigint FROM devices WHERE is_virtual AND deleted_at IS NULL;

-- name: CountDevices :one
-- Total live devices (for the dashboard total / discovered split).
SELECT COUNT(*)::bigint FROM devices WHERE deleted_at IS NULL;

-- name: DeleteManualDeviceRoles :exec
-- Clear operator-entered roles before re-writing them (virtual-device edit).
DELETE FROM device_roles WHERE device_id = $1 AND source = 'manual';
