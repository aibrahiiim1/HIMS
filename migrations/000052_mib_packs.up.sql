-- MIB packs: vendor SNMP MIB bundles that DRIVE collection (not just a browser).
-- Modeled on vendor_fingerprints: built-in ∪ user-defined, enabled + priority,
-- applies-to matching (sysObjectID prefix / sysDescr substring / category), and a
-- mapping layer (table → root OID → purpose → column map) so HIMS knows which OID
-- feeds which domain table. User packs override built-in by priority.

CREATE TABLE mib_packs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL,
    vendor           TEXT NOT NULL DEFAULT '',
    category         TEXT NOT NULL DEFAULT '',  -- e.g. wireless_controller
    source           TEXT NOT NULL DEFAULT 'user' CHECK (source IN ('builtin','user')),
    enabled          BOOLEAN NOT NULL DEFAULT true,
    priority         INTEGER NOT NULL DEFAULT 100, -- lower runs first; user beats builtin at ties
    version          TEXT NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    -- {sysobjectid_prefixes:[], sysdescr_contains:[], categories:[]}
    applies_to       JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- {modules:[], files:[], tables:[], object_count, table_count, warnings:[]}
    parse_meta       JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_tested_at   TIMESTAMPTZ,
    last_test_detail TEXT NOT NULL DEFAULT '',
    last_matched_device UUID,
    last_collected_at   TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, name)
);
CREATE INDEX idx_mib_packs_priority ON mib_packs (priority);

-- Raw uploaded MIB files per pack (kept for re-parse + download).
CREATE TABLE mib_pack_files (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id      UUID NOT NULL REFERENCES mib_packs(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    module_name  TEXT NOT NULL DEFAULT '',
    content      BYTEA NOT NULL DEFAULT '\x'::bytea,
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    parse_status TEXT NOT NULL DEFAULT 'ok',  -- ok | parse_error | unparsed
    parse_detail TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (pack_id, filename)
);

-- Collection-target mappings: table → root OID → purpose → column map.
CREATE TABLE mib_pack_tables (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id     UUID NOT NULL REFERENCES mib_packs(id) ON DELETE CASCADE,
    table_name  TEXT NOT NULL,
    root_oid    TEXT NOT NULL,
    purpose     TEXT NOT NULL DEFAULT '', -- controller_identity|aps|ssids|clients|radios|events|interfaces|stats
    -- {ap_name:<col>, ap_mac:<col>, ssid_name:<col>, client_mac:<col>, rssi:<col>, channel:<col>, ...}
    column_map  JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (pack_id, table_name)
);
CREATE INDEX idx_mib_pack_tables_pack ON mib_pack_tables (pack_id);

-- Raw walked rows from the most recent walk per device+table (troubleshooting).
CREATE TABLE mib_walk_rows (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    pack_id      UUID REFERENCES mib_packs(id) ON DELETE SET NULL,
    table_name   TEXT NOT NULL,
    oid          TEXT NOT NULL,
    idx          TEXT NOT NULL DEFAULT '',
    raw_value    TEXT NOT NULL DEFAULT '',
    collected_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_mib_walk_rows_dev ON mib_walk_rows (device_id, table_name, collected_at DESC);
