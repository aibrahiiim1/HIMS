-- Per-capability wireless collection health. The Add Controller driver catalog
-- declares what each controller type CAN collect; this table records what each
-- capability ACTUALLY did on the last real collection, so the UI can show an
-- honest status per feature (collected / endpoint_not_exposed / auth_failed /
-- unsupported_by_device / needs_configuration / not_implemented) instead of a
-- single per-controller pass/fail. One row per (controller device, capability).
CREATE TABLE IF NOT EXISTS wireless_collection_health (
    controller_device_id UUID        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    capability           TEXT        NOT NULL, -- aps | ssids | clients | radios | health | firmware
    status               TEXT        NOT NULL, -- collected | endpoint_not_exposed | auth_failed | unsupported_by_device | needs_configuration | not_implemented
    detail               TEXT        NOT NULL DEFAULT '',
    row_count            INTEGER     NOT NULL DEFAULT 0,
    source               TEXT        NOT NULL DEFAULT '',
    collected_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (controller_device_id, capability)
);

CREATE INDEX IF NOT EXISTS idx_wireless_health_device
    ON wireless_collection_health (controller_device_id);
