-- name: ListWebPortCandidates :many
SELECT * FROM web_port_candidates ORDER BY port;

-- name: ListEnabledWebPortCandidates :many
SELECT * FROM web_port_candidates WHERE enabled ORDER BY port;

-- name: CreateWebPortCandidate :one
INSERT INTO web_port_candidates (port, scheme, enabled, note)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateWebPortCandidate :one
UPDATE web_port_candidates
SET port = $2, scheme = $3, enabled = $4, note = $5
WHERE id = $1
RETURNING *;

-- name: DeleteWebPortCandidate :exec
DELETE FROM web_port_candidates WHERE id = $1;
