-- NVR/DVR recorder inventory beyond the camera_info shape: recorder identity
-- (model/serial/firmware/deviceType + channel & HDD counts + recording/health
-- summary) and per-HDD storage status. Camera/channel rows already live in
-- nvr_channels (000015). All populated read-only from Hikvision ISAPI.

CREATE TABLE nvr_info (
    device_id     UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    manufacturer  TEXT,
    model         TEXT,
    serial        TEXT,
    firmware      TEXT,
    device_type   TEXT,                       -- raw ISAPI deviceType: NVR | DVR | IPCamera
    channel_count INT  NOT NULL DEFAULT 0,
    hdd_count     INT  NOT NULL DEFAULT 0,
    recording     TEXT NOT NULL DEFAULT '',   -- summary, '' = not exposed by device
    health        TEXT NOT NULL DEFAULT '',   -- working-status summary, '' = not exposed
    source        TEXT NOT NULL DEFAULT 'isapi',
    collected_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE nvr_storage (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nvr_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    hdd_id        INT  NOT NULL,
    name          TEXT,
    status        TEXT   NOT NULL DEFAULT 'unknown', -- ok | error | unformatted | sleeping | ...
    capacity_mb   BIGINT NOT NULL DEFAULT 0,
    free_mb       BIGINT NOT NULL DEFAULT 0,
    property      TEXT   NOT NULL DEFAULT '',         -- RW | RO | redundancy
    source        TEXT   NOT NULL DEFAULT 'isapi',
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (nvr_device_id, hdd_id)
);
CREATE INDEX idx_nvr_storage_nvr ON nvr_storage (nvr_device_id);

-- nvr_channels gains an explicit enabled flag (configured vs merely present).
ALTER TABLE nvr_channels ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT true;
