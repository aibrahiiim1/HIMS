-- HIMS Encryption Key Lifecycle Management.
--
-- Stores ONLY key metadata — never the raw key. The key continues to live
-- exclusively in HIMS_ENCRYPTION_KEY (process env). This table records the
-- fingerprint + lifecycle timestamps so the UI can show encryption health,
-- detect fingerprint mismatch, and drive rotation/recovery.

CREATE TABLE encryption_metadata (
    id              INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1), -- singleton row
    fingerprint     TEXT NOT NULL DEFAULT '',   -- full SHA-256 colon-hex of the key
    key_id          TEXT NOT NULL DEFAULT '',   -- short blob-tag id (first 8 hex)
    algorithm       TEXT NOT NULL DEFAULT 'AES-256-GCM',
    version         INTEGER NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_rotation_at   TIMESTAMPTZ,
    last_validation_at TIMESTAMPTZ
);

-- Credentials whose encrypted secret was cleared (lost-key recovery) and must
-- be re-entered by an operator. Records, metadata, assignments and group
-- memberships are preserved — only the secret field is reset.
ALTER TABLE credentials
    ADD COLUMN needs_secret_reentry BOOLEAN NOT NULL DEFAULT false;
