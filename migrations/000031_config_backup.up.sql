-- Config Backup (#10) + Config Drift (#11): versioned device running-configs.
-- The config text is a secret (it can contain SNMP communities, pre-shared
-- keys, local hashes), so it is stored AES-256-GCM encrypted (same cipher as
-- credentials) and is returned by the API only on an explicit, key-gated
-- content/diff request — never in list responses, never logged.

CREATE TABLE config_backups (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    captured_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    captured_by       TEXT NOT NULL DEFAULT 'operator',
    source            TEXT NOT NULL DEFAULT 'ssh' CHECK (source IN ('ssh','manual','scheduled')),
    driver            TEXT NOT NULL DEFAULT '',     -- device driver at capture time
    command           TEXT NOT NULL DEFAULT '',     -- CLI command used to dump the config
    content_encrypted BYTEA NOT NULL,               -- AES-GCM blob of the running-config (secret)
    key_id            TEXT NOT NULL,
    sha256            TEXT NOT NULL,                 -- hash of NORMALISED content, for drift detection
    size_bytes        INT NOT NULL DEFAULT 0,       -- plaintext size (metadata, not sensitive)
    changed           BOOLEAN NOT NULL DEFAULT true  -- sha256 differs from the previous backup of this device
);

-- Per-device version history, newest first; also the drift "latest hash" lookup.
CREATE INDEX idx_config_backups_device ON config_backups (device_id, captured_at DESC);
