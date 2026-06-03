-- name: UpsertPrinterSupply :exec
INSERT INTO printer_supplies (device_id, supply_index, description, level, max_capacity, pct, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (device_id, supply_index) DO UPDATE SET
    description = EXCLUDED.description,
    level = EXCLUDED.level,
    max_capacity = EXCLUDED.max_capacity,
    pct = EXCLUDED.pct,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListPrinterSupplies :many
SELECT * FROM printer_supplies WHERE device_id = $1 ORDER BY supply_index;

-- name: DeleteStalePrinterSupplies :exec
DELETE FROM printer_supplies
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;
