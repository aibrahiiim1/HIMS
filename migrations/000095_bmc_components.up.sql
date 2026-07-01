-- Detailed Redfish hardware inventory beyond the bmc_info summary + fan/PSU/temp sensors:
-- processors (CPU), memory DIMMs, storage controllers (RAID), volumes (RAID arrays), and
-- physical drives (HDD/SSD). Populated by AUTHENTICATED Redfish collection only. bmc_info
-- gains a CPU/RAM/BIOS summary so the overview shows compute at a glance.

ALTER TABLE bmc_info ADD COLUMN IF NOT EXISTS cpu_model TEXT;
ALTER TABLE bmc_info ADD COLUMN IF NOT EXISTS cpu_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bmc_info ADD COLUMN IF NOT EXISTS cpu_cores INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bmc_info ADD COLUMN IF NOT EXISTS memory_gib DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE bmc_info ADD COLUMN IF NOT EXISTS bios_version TEXT;

CREATE TABLE IF NOT EXISTS bmc_components (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL,              -- cpu | memory | controller | volume | drive
    name              TEXT NOT NULL,              -- socket/locator/name (stable within kind)
    model             TEXT,
    serial            TEXT,
    status            TEXT,                       -- OK | Warning | Critical | (raw Redfish health)
    capacity_bytes    BIGINT NOT NULL DEFAULT 0,  -- DIMM/drive/volume capacity; 0 for CPU
    detail            JSONB NOT NULL DEFAULT '{}',-- kind-specific: cores/threads/speed, media_type/protocol/rpm, raid_type, firmware…
    collection_source TEXT NOT NULL DEFAULT 'redfish',
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, kind, name)
);
CREATE INDEX IF NOT EXISTS idx_bmc_components_device ON bmc_components (device_id);
