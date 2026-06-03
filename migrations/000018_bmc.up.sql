-- HIMS Redfish phase: out-of-band BMC inventory + hardware health. bmc_info
-- is the per-server controller summary; bmc_sensors holds fan/PSU/temperature/
-- storage health readings. Populated by the Redfish (iLO/iDRAC) collector.

CREATE TABLE bmc_info (
    device_id        UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    vendor           TEXT,
    controller_kind  TEXT,            -- iLO | iDRAC
    model            TEXT,
    serial           TEXT,
    firmware_version TEXT,
    power_state      TEXT,
    health           TEXT,            -- OK | Warning | Critical
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE bmc_sensors (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,       -- fan | psu | temperature | storage
    name         TEXT NOT NULL,
    status       TEXT,
    reading      DOUBLE PRECISION,
    unit         TEXT,
    has_reading  BOOLEAN NOT NULL DEFAULT false,
    collection_source TEXT NOT NULL DEFAULT 'redfish',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, kind, name)
);

CREATE INDEX idx_bmc_sensors_device ON bmc_sensors (device_id);
