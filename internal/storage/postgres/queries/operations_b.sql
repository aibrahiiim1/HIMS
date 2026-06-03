-- ---- Spare parts ----------------------------------------------------------

-- name: CreateSparePart :one
INSERT INTO spare_parts (name, sku, category, location_id, quantity, min_quantity, unit_cost, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *;

-- name: GetSparePart :one
SELECT * FROM spare_parts WHERE id = $1;

-- name: ListSpareParts :many
SELECT * FROM spare_parts ORDER BY name LIMIT 500;

-- name: ListLowStockParts :many
SELECT * FROM spare_parts WHERE quantity <= min_quantity ORDER BY name;

-- name: UpdateSparePart :one
UPDATE spare_parts SET
    name = $2, sku = $3, category = $4, location_id = $5,
    min_quantity = $6, unit_cost = $7, notes = $8, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: AdjustSparePartStock :one
-- Absolute set of on-hand quantity (a stock count / receiving correction).
-- The CHECK (quantity >= 0) constraint rejects negative results.
UPDATE spare_parts SET quantity = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteSparePart :exec
DELETE FROM spare_parts WHERE id = $1;

-- ---- Work-order parts (stock consumption) ---------------------------------

-- name: ConsumePartToWorkOrder :one
-- Atomic: decrement stock AND record the consumption in ONE statement. The
-- UPDATE's WHERE quantity >= $3 is the precondition; if it fails to match,
-- the CTE yields no row, the INSERT inserts nothing, and :one returns
-- ErrNoRows — which the handler maps to "insufficient stock" (409). This is
-- the atomic-DB-signal pattern: no SELECT-then-UPDATE TOCTOU window.
WITH dec AS (
    UPDATE spare_parts SET quantity = quantity - $3, updated_at = now()
    WHERE spare_parts.id = $2 AND spare_parts.quantity >= $3
    RETURNING spare_parts.id AS part_id, spare_parts.unit_cost AS part_cost
)
INSERT INTO work_order_parts (work_order_id, spare_part_id, description, quantity, unit_cost)
SELECT $1, dec.part_id, $4, $3, dec.part_cost FROM dec
RETURNING *;

-- name: AddFreeWorkOrderPart :one
-- A part not tracked in stock (free-text): just record it, no decrement.
INSERT INTO work_order_parts (work_order_id, spare_part_id, description, quantity, unit_cost)
VALUES ($1, NULL, $2, $3, $4)
RETURNING *;

-- name: ListWorkOrderParts :many
SELECT * FROM work_order_parts WHERE work_order_id = $1 ORDER BY created_at;

-- ---- Purchases ------------------------------------------------------------

-- name: CreatePurchase :one
INSERT INTO purchases (description, vendor, category, location_id, system_id, device_id, amount, purchased_at, invoice_ref, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
RETURNING *;

-- name: ListPurchases :many
SELECT * FROM purchases ORDER BY purchased_at DESC, created_at DESC LIMIT 500;

-- name: DeletePurchase :exec
DELETE FROM purchases WHERE id = $1;

-- ---- Expenses (aggregation over the purchases ledger) ---------------------
-- Expenses derive from the purchases ledger so the totals can never drift
-- from their source rows. Work-order cost + system cost stay on their own
-- pages (not merged here, to avoid double-counting a purchase logged for the
-- same repair/license).

-- name: ExpensesByCategory :many
SELECT category, COALESCE(SUM(amount),0)::double precision AS total, COUNT(*) AS count
FROM purchases
GROUP BY category
ORDER BY total DESC;

-- name: ExpensesByLocation :many
SELECT p.location_id, l.name AS location_name,
       COALESCE(SUM(p.amount),0)::double precision AS total, COUNT(*) AS count
FROM purchases p
LEFT JOIN locations l ON l.id = p.location_id
GROUP BY p.location_id, l.name
ORDER BY total DESC;
