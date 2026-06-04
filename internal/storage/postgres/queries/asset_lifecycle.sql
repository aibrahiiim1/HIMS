-- name: UpsertDeviceLifecycle :one
INSERT INTO device_lifecycle (device_id, owner, supplier, purchase_date, warranty_expiry, eol_date, cost, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (device_id) DO UPDATE SET
    owner = EXCLUDED.owner,
    supplier = EXCLUDED.supplier,
    purchase_date = EXCLUDED.purchase_date,
    warranty_expiry = EXCLUDED.warranty_expiry,
    eol_date = EXCLUDED.eol_date,
    cost = EXCLUDED.cost,
    notes = EXCLUDED.notes,
    updated_at = now()
RETURNING *;

-- name: GetDeviceLifecycle :one
SELECT * FROM device_lifecycle WHERE device_id = $1;

-- name: DeleteDeviceLifecycle :exec
DELETE FROM device_lifecycle WHERE device_id = $1;

-- name: ListAssetLifecycle :many
-- The asset register: every tracked asset (has a lifecycle row) with its device
-- identity, ordered by soonest warranty expiry.
SELECT l.device_id, d.name AS device_name, d.category, d.primary_ip,
       l.owner, l.supplier, l.purchase_date, l.warranty_expiry, l.eol_date, l.cost, l.notes
FROM device_lifecycle l
JOIN devices d ON d.id = l.device_id
ORDER BY l.warranty_expiry ASC NULLS LAST, d.name;
