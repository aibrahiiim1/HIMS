-- name: CreateCredential :one
INSERT INTO credentials (name, kind, encrypted_blob, key_id, weak, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCredential :one
SELECT * FROM credentials WHERE id = $1;

-- name: ListCredentials :many
SELECT * FROM credentials ORDER BY name;

-- name: UpdateCredentialSecret :exec
-- Used by key rotation: re-seal the secret under a new key + KeyID.
UPDATE credentials SET encrypted_blob = $2, key_id = $3, updated_at = now()
WHERE id = $1;

-- name: CreateCredentialGroup :one
INSERT INTO credential_groups (name, description)
VALUES ($1, $2)
RETURNING *;

-- name: AddCredentialGroupMember :exec
INSERT INTO credential_group_members (group_id, credential_id, priority)
VALUES ($1, $2, $3)
ON CONFLICT (group_id, credential_id) DO UPDATE SET priority = EXCLUDED.priority;

-- name: BindCredentialGroup :one
INSERT INTO credential_bindings (group_id, location_id, subnet_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ResolveCandidatesForIP :many
-- The resolver-assembly query: for a device IP, return every credential in a
-- group bound to either a subnet that contains the IP (more specific) or a
-- location anchor, with the binding specificity + member priority so the
-- pure resolver (internal/credresolver) can order them.
--   specificity 2 = subnet binding, 1 = location binding.
SELECT
    c.id,
    c.kind,
    c.weak,
    m.priority,
    CASE WHEN b.subnet_id IS NOT NULL THEN 2 ELSE 1 END AS specificity
FROM credential_bindings b
JOIN credential_groups g ON g.id = b.group_id
JOIN credential_group_members m ON m.group_id = g.id
JOIN credentials c ON c.id = m.credential_id
LEFT JOIN subnets s ON s.id = b.subnet_id
WHERE
    (b.subnet_id IS NOT NULL AND $1::inet << s.cidr)
    OR (b.location_id IS NOT NULL AND b.location_id = $2);
