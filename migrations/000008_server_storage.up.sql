-- HIMS Phase 3a: server storage (filesystems / RAM) from HOST-RESOURCES-MIB.
--
-- One row per hrStorageTable entry (RAM, /, /var, C:, …). CPU-count and
-- total-RAM scalars live in device_facts; this table holds the per-volume
-- capacity/usage that a server detail page lists. Same source-scoped
-- upsert+prune pattern as the network-inventory tables.

CREATE TABLE server_storage (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    hr_index          INTEGER NOT NULL,
    descr             TEXT,
    -- storage_type: ram | disk | virtual | other (normalized from hrStorageType).
    storage_type      TEXT NOT NULL DEFAULT 'other',
    total_bytes       BIGINT,
    used_bytes        BIGINT,
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, hr_index)
);

CREATE INDEX idx_server_storage_device_source ON server_storage (device_id, collection_source);
