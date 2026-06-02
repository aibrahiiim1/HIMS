-- name: CreateWorkOrder :one
INSERT INTO work_orders (
    device_id, location_id, title, problem_type, priority, status,
    assigned_to, diagnosis, action_taken, spare_parts, external_vendor, cost
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING *;

-- name: GetWorkOrder :one
SELECT * FROM work_orders WHERE id = $1;

-- name: ListWorkOrders :many
SELECT * FROM work_orders ORDER BY
    CASE status WHEN 'open' THEN 0 WHEN 'in_progress' THEN 1 WHEN 'waiting' THEN 2 ELSE 3 END,
    CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
    created_at DESC
LIMIT 200;

-- name: ListWorkOrdersByDevice :many
SELECT * FROM work_orders WHERE device_id = $1 ORDER BY created_at DESC;

-- name: UpdateWorkOrder :one
UPDATE work_orders SET
    status        = $2,
    priority      = $3,
    assigned_to   = $4,
    diagnosis     = $5,
    action_taken  = $6,
    spare_parts   = $7,
    external_vendor = $8,
    cost          = $9,
    resolved_at   = CASE WHEN $2 IN ('solved','closed') AND resolved_at IS NULL THEN now() ELSE resolved_at END,
    updated_at    = now()
WHERE id = $1
RETURNING *;

-- name: AddWorkOrderEvent :one
INSERT INTO work_order_events (work_order_id, event_type, note, actor)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListWorkOrderEvents :many
SELECT * FROM work_order_events WHERE work_order_id = $1 ORDER BY created_at;

-- name: CreateSystem :one
INSERT INTO systems (name, vendor, location_id, license_expiry, support_expiry, cost, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING *;

-- name: ListSystems :many
SELECT * FROM systems ORDER BY license_expiry NULLS LAST, name;

-- name: GetSystem :one
SELECT * FROM systems WHERE id = $1;

-- name: UpdateSystem :one
UPDATE systems SET
    name = $2, vendor = $3, location_id = $4,
    license_expiry = $5, support_expiry = $6, cost = $7, notes = $8,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteSystem :exec
DELETE FROM systems WHERE id = $1;
