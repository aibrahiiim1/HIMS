-- name: UpsertServerStorage :exec
INSERT INTO server_storage (
    device_id, hr_index, descr, storage_type, total_bytes, used_bytes,
    collection_source, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (device_id, hr_index) DO UPDATE SET
    descr = EXCLUDED.descr,
    storage_type = EXCLUDED.storage_type,
    total_bytes = EXCLUDED.total_bytes,
    used_bytes = EXCLUDED.used_bytes,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = now();

-- name: ListServerStorage :many
SELECT * FROM server_storage WHERE device_id = $1 ORDER BY hr_index;

-- name: DeleteStaleServerStorage :exec
DELETE FROM server_storage
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;
