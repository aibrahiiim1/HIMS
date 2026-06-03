-- HIMS voice: PBX phone registry (Cisco CUCM AXL listPhone). One row per phone
-- registered to a call-manager device; refreshed each collection with stale
-- prune by last_seen_at.

CREATE TABLE pbx_phones (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id      UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    model          TEXT,
    description    TEXT,
    device_pool    TEXT,
    collection_source TEXT NOT NULL DEFAULT 'axl',
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, name)
);

CREATE INDEX idx_pbx_phones_device ON pbx_phones (device_id);
