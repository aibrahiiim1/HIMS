-- name: CreateMibFile :one
INSERT INTO mib_files (name, object_count, unresolved)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListMibFiles :many
SELECT * FROM mib_files ORDER BY uploaded_at DESC;

-- name: InsertMibObject :exec
INSERT INTO mib_objects (mib_file_id, name, oid, syntax, kind, unresolved)
VALUES ($1,$2,$3,$4,$5,$6);

-- name: ListMibObjects :many
SELECT * FROM mib_objects WHERE mib_file_id = $1 ORDER BY oid;

-- name: SearchMibObjects :many
SELECT * FROM mib_objects WHERE name ILIKE $1 OR oid LIKE $1 ORDER BY oid LIMIT 200;

-- name: CreateOIDMapping :one
INSERT INTO oid_mappings (oid, label, metric_key, vendor, template, notes)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (oid, metric_key) DO UPDATE SET
    label = EXCLUDED.label, vendor = EXCLUDED.vendor,
    template = EXCLUDED.template, notes = EXCLUDED.notes
RETURNING *;

-- name: ListOIDMappings :many
SELECT * FROM oid_mappings ORDER BY created_at DESC;

-- name: DeleteOIDMapping :exec
DELETE FROM oid_mappings WHERE id = $1;
