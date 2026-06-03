-- name: ListLookups :many
SELECT * FROM lookups WHERE kind = $1 ORDER BY value;

-- name: CreateLookup :one
INSERT INTO lookups (kind, value) VALUES ($1, $2)
ON CONFLICT (kind, value) DO UPDATE SET value = EXCLUDED.value
RETURNING *;

-- name: DeleteLookup :exec
DELETE FROM lookups WHERE id = $1;
