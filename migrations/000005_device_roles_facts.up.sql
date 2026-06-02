-- HIMS Phase 0: multi-role devices + per-driver facts.
--
-- device_roles: a box can hold several roles at once (e.g. a Windows server
-- that is Hyper-V Host + Domain Controller + DNS + DHCP). Each role is a
-- row; the detail page composes sections from all of them.
--
-- device_facts: the generic key/value/JSON store for vendor-specific data
-- that doesn't deserve a core column (ADR 0001). Normalized facts a driver
-- emits (e.g. hardware.temperature, ha.sync_status, license.av_expiry) plus
-- a raw snapshot for audit/debug.

CREATE TABLE device_roles (
    device_id  UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    role       TEXT NOT NULL
        CHECK (role IN (
            'hyperv_host','esxi_host','domain_controller','dns','dhcp',
            'sql_server','oracle','postgresql','file_server','web_server',
            'wireless_controller','voice','router','firewall')),
    -- The driver/source that asserted this role (so a reclassify can prune
    -- its own roles without touching operator-pinned ones).
    source     TEXT NOT NULL DEFAULT 'discovery',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, role)
);

CREATE INDEX idx_device_roles_role ON device_roles (role);

CREATE TABLE device_facts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id   UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    -- Normalized, dotted fact name (e.g. "hardware.serial",
    -- "ha.sync_status"). Stable across vendors; drivers map their raw OIDs /
    -- API fields onto these names.
    key         TEXT NOT NULL,
    value       TEXT,
    -- Optional structured value when a fact is more than a scalar.
    value_json  JSONB,
    -- Which driver produced this fact + when, for source-scoped refresh.
    driver      TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, key)
);

CREATE INDEX idx_device_facts_device ON device_facts (device_id);
CREATE INDEX idx_device_facts_key ON device_facts (key);
