-- name: ListDeviceCategories :many
-- All categories with a live device count, ordered for the picker + settings UI.
SELECT c.value, c.label, c.icon, c.builtin, c.enabled, c.sort_order, c.created_at, c.updated_at,
       (SELECT count(*) FROM devices d WHERE d.category = c.value AND d.deleted_at IS NULL) AS device_count
FROM device_categories c
ORDER BY c.sort_order, c.value;

-- name: GetDeviceCategory :one
SELECT * FROM device_categories WHERE value = $1;

-- name: CreateDeviceCategory :one
INSERT INTO device_categories (value, label, icon, builtin, enabled, sort_order)
VALUES ($1, $2, $3, false, true, $4)
RETURNING *;

-- name: UpdateDeviceCategory :one
-- Cosmetic/visibility fields only; value + builtin are immutable.
UPDATE device_categories
SET label = $2, icon = $3, enabled = $4, sort_order = $5, updated_at = now()
WHERE value = $1
RETURNING *;

-- name: DeleteDeviceCategory :exec
-- Guarded in the handler: only builtin=false rows are ever passed here.
DELETE FROM device_categories WHERE value = $1 AND builtin = false;

-- name: CountDevicesInCategory :one
SELECT count(*) FROM devices WHERE category = $1 AND deleted_at IS NULL;

-- name: ReassignDeviceCategory :exec
UPDATE devices SET category = $2, updated_at = now() WHERE category = $1 AND deleted_at IS NULL;

-- name: IsCategoryValid :one
-- App-level replacement for the dropped CHECK constraint: a category is valid if a
-- row exists for it (built-in or custom). Enabled/disabled does not affect validity
-- (a hidden built-in a device already has stays valid; hiding only removes it from pickers).
SELECT EXISTS(SELECT 1 FROM device_categories WHERE value = $1);
