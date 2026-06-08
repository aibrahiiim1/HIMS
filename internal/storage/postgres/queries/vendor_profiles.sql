-- Vendor connection profile CRUD + resolution. No secrets are stored here; the
-- credential reference points at the encrypted credentials table.

-- name: CreateVendorProfile :one
INSERT INTO vendor_connection_profiles (name, vendor_type, target_url, credential_id, location_id, device_id, config, enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateVendorProfile :one
UPDATE vendor_connection_profiles
SET name = $2, vendor_type = $3, target_url = $4, credential_id = $5,
    location_id = $6, device_id = $7, config = $8, enabled = $9, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteVendorProfile :exec
DELETE FROM vendor_connection_profiles WHERE id = $1;

-- name: GetVendorProfile :one
SELECT * FROM vendor_connection_profiles WHERE id = $1;

-- name: GetVendorProfileForDeviceVendor :one
-- The (device, vendor_type) profile if one exists (any enabled state) — keeps the
-- "Add controller" flow idempotent (update in place rather than create a duplicate).
SELECT * FROM vendor_connection_profiles
WHERE device_id = $1 AND vendor_type = $2
ORDER BY created_at LIMIT 1;

-- name: CountVendorProfilesUsingCredential :one
-- How many profiles still reference a credential (for orphan-credential cleanup on
-- profile delete).
SELECT count(*) FROM vendor_connection_profiles WHERE credential_id = $1;

-- name: ListVendorProfiles :many
SELECT * FROM vendor_connection_profiles ORDER BY vendor_type, name;

-- name: SetVendorProfileTest :exec
UPDATE vendor_connection_profiles
SET last_test_at = now(), last_test_ok = $2, last_test_detail = $3,
    status = CASE WHEN $2 THEN 'ok' ELSE 'failed' END, updated_at = now()
WHERE id = $1;

-- name: SetVendorProfileCollection :exec
UPDATE vendor_connection_profiles
SET last_collection_at = now(), last_collection_detail = $2, updated_at = now()
WHERE id = $1;

-- name: ResolveVendorProfiles :many
-- Profiles applicable to a device during a scan: a profile bound to this exact
-- device, or one bound to this location (site-level), or an unbound global
-- profile — for the given vendor_type. Most specific first.
SELECT * FROM vendor_connection_profiles
WHERE enabled
  AND vendor_type = $1
  AND (device_id = $2 OR location_id = $3 OR (device_id IS NULL AND location_id IS NULL))
ORDER BY (device_id = $2) DESC, (location_id = $3) DESC;
