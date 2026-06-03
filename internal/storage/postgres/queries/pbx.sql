-- name: UpsertPbxPhone :exec
INSERT INTO pbx_phones (device_id, name, model, description, device_pool, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (device_id, name) DO UPDATE SET
    model = EXCLUDED.model,
    description = EXCLUDED.description,
    device_pool = EXCLUDED.device_pool,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListPbxPhones :many
SELECT * FROM pbx_phones WHERE device_id = $1 ORDER BY name;

-- name: DeleteStalePbxPhones :exec
DELETE FROM pbx_phones
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;
