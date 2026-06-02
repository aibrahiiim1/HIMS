-- HIMS Phase 0: the generic device core.
--
-- ONE devices table for every category. No per-vendor tables (ADR 0001).
-- Category names the kind of thing; roles (000005) allow multi-role boxes;
-- vendor specifics live in device_facts (000005), not here.

CREATE TABLE devices (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Where it physically lives (room/rack/building/…). NULL until placed.
    location_id   UUID REFERENCES locations(id) ON DELETE SET NULL,
    -- Primary management IP. INET for native, indexable storage. NULL for
    -- manually-added assets with no network identity yet.
    primary_ip    INET,
    hostname      TEXT,
    name          TEXT NOT NULL,
    vendor        TEXT,
    model         TEXT,
    serial        TEXT,
    os_version    TEXT,
    -- Category drives which template renders the detail page.
    category      TEXT NOT NULL DEFAULT 'unknown'
        CHECK (category IN (
            'unknown','switch','router','firewall','access_point',
            'wireless_controller','server','virtual_host','virtual_machine',
            'storage','nvr','camera','printer','ip_phone','pbx',
            'voice_gateway','database','directory','dns','dhcp',
            'fingerprint','endpoint','ups','isp_router','application')),
    -- Reachability/health rollup: up / down / warning / unknown.
    status        TEXT NOT NULL DEFAULT 'unknown'
        CHECK (status IN ('up','down','warning','unknown')),
    -- The driver that classified/collects this device (e.g. 'aruba_hpe').
    driver        TEXT,
    -- Bind-on-success: the credential that last authenticated. The resolver
    -- tries this first before walking scope candidates.
    credential_id UUID REFERENCES credentials(id) ON DELETE SET NULL,
    last_discovery_at  TIMESTAMPTZ,
    last_monitoring_at TIMESTAMPTZ,
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

-- Identity reconciliation: a live device is unique by (primary_ip,
-- location) — the multi-hotel-safe key (the same IP can recur in different
-- hotels). Partial so soft-deleted rows don't block re-adds.
CREATE UNIQUE INDEX idx_devices_ip_location_live
    ON devices (primary_ip, location_id)
    WHERE deleted_at IS NULL AND primary_ip IS NOT NULL;

CREATE INDEX idx_devices_category ON devices (category) WHERE deleted_at IS NULL;
CREATE INDEX idx_devices_location ON devices (location_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_devices_status ON devices (status) WHERE deleted_at IS NULL;
CREATE INDEX idx_devices_primary_ip ON devices (primary_ip) WHERE deleted_at IS NULL;
