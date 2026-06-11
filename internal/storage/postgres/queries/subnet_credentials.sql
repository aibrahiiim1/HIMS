-- name: ListSubnetCredentials :many
-- Credentials assigned to a subnet (for the editor + indicators).
SELECT c.id, c.name, c.kind
FROM subnet_credentials sc
JOIN credentials c ON c.id = sc.credential_id
WHERE sc.subnet_id = $1
ORDER BY c.kind, c.name;

-- name: AddSubnetCredential :exec
INSERT INTO subnet_credentials (subnet_id, credential_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ClearSubnetCredentials :exec
DELETE FROM subnet_credentials WHERE subnet_id = $1;

-- name: SubnetCredentialCounts :many
-- Per-subnet assignment count + distinct kinds for a location (UI row badges).
SELECT s.id AS subnet_id,
       count(sc.credential_id)::bigint AS cred_count,
       COALESCE(array_agg(DISTINCT c.kind) FILTER (WHERE c.kind IS NOT NULL), '{}')::text[] AS kinds
FROM subnets s
LEFT JOIN subnet_credentials sc ON sc.subnet_id = s.id
LEFT JOIN credentials c ON c.id = sc.credential_id
WHERE s.location_id = $1
GROUP BY s.id;

-- name: SubnetScopedCredentialsForIP :many
-- The EXCLUSIVE credential set for the most-specific site subnet that (a) contains
-- the IP and (b) has assignments. Empty result ⇒ no subnet scoping ⇒ caller falls
-- back to normal resolution. location_id is optional (NULL = match any location).
WITH match AS (
    SELECT s.id, s.name, s.cidr, masklen(s.cidr) AS ml
    FROM subnets s
    WHERE (sqlc.narg('location_id')::uuid IS NULL OR s.location_id = sqlc.narg('location_id'))
      AND s.cidr >>= sqlc.arg('ip')::inet
      AND EXISTS (SELECT 1 FROM subnet_credentials sc WHERE sc.subnet_id = s.id)
    ORDER BY masklen(s.cidr) DESC
    LIMIT 1
)
SELECT c.id, c.kind, m.name AS subnet_name, (host(m.cidr) || '/' || m.ml)::text AS cidr
FROM match m
JOIN subnet_credentials sc ON sc.subnet_id = m.id
JOIN credentials c ON c.id = sc.credential_id
ORDER BY c.kind;
