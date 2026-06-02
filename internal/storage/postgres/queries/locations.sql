-- name: CreateLocation :one
INSERT INTO locations (parent_id, kind, name, code, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetLocation :one
SELECT * FROM locations WHERE id = $1;

-- name: ListChildLocations :many
SELECT * FROM locations WHERE parent_id = $1 ORDER BY kind, name;

-- name: ListRootLocations :many
SELECT * FROM locations WHERE parent_id IS NULL ORDER BY name;

-- name: DeleteLocation :exec
DELETE FROM locations WHERE id = $1;
