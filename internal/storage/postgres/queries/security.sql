-- ===== Encryption key lifecycle (metadata only — never the key) ===========

-- name: GetEncryptionMetadata :one
SELECT * FROM encryption_metadata WHERE id = 1;

-- name: UpsertEncryptionMetadata :exec
-- Adopt / record the current key's fingerprint (generate + first-run adopt).
INSERT INTO encryption_metadata (id, fingerprint, key_id, version)
VALUES (1, $1, $2, 1)
ON CONFLICT (id) DO UPDATE SET fingerprint = $1, key_id = $2;

-- name: RecordRotation :exec
-- Bump version + stamp the rotation; sets the new key's fingerprint.
INSERT INTO encryption_metadata (id, fingerprint, key_id, version, last_rotation_at)
VALUES (1, $1, $2, 1, now())
ON CONFLICT (id) DO UPDATE SET
    fingerprint = $1, key_id = $2,
    version = encryption_metadata.version + 1,
    last_rotation_at = now();

-- name: TouchValidation :exec
UPDATE encryption_metadata SET last_validation_at = now() WHERE id = 1;

-- ===== Credential secret accounting / recovery =============================

-- name: CountEncryptedCredentials :one
SELECT COUNT(*) FROM credentials WHERE octet_length(encrypted_blob) > 0;

-- name: CountCredentialsNeedingReentry :one
SELECT COUNT(*) FROM credentials WHERE needs_secret_reentry;

-- name: CountUndecryptableCredentials :one
-- Blobs sealed under a key id other than the one currently loaded.
SELECT COUNT(*) FROM credentials
WHERE octet_length(encrypted_blob) > 0 AND key_id <> $1;

-- name: ListCredentialBlobs :many
SELECT id, name, kind, encrypted_blob, key_id
FROM credentials
WHERE octet_length(encrypted_blob) > 0
ORDER BY name;

-- name: ClearAllCredentialSecrets :exec
-- Lost-key recovery: wipe only the secret fields; preserve the record,
-- metadata, assignments and group memberships. Flag for re-entry.
UPDATE credentials
SET encrypted_blob = ''::bytea, key_id = '', needs_secret_reentry = true, updated_at = now()
WHERE octet_length(encrypted_blob) > 0;

-- name: ListCredentialsNeedingReentry :many
SELECT id, name, kind, weak, needs_secret_reentry, created_at, updated_at
FROM credentials WHERE needs_secret_reentry ORDER BY name;

-- name: ClearReentryFlag :exec
UPDATE credentials SET needs_secret_reentry = false WHERE id = $1;
