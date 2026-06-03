-- HIMS Phase 7: CCTV. camera_info holds per-camera detail; nvr_channels maps
-- an NVR's channels to cameras. Populated by the ONVIF/vendor-REST transport
-- (deferred); the tables + read paths ship now so the camera/NVR detail pages
-- have their sections and the channel→camera mapping has a home.

CREATE TABLE camera_info (
    device_id    UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    manufacturer TEXT,
    model        TEXT,
    resolution   TEXT,
    rtsp_url     TEXT,
    onvif_url    TEXT,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE nvr_channels (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nvr_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    channel_no    INT NOT NULL,
    camera_name   TEXT,
    camera_ip     INET,
    -- Optional link to the camera's own managed device row.
    camera_device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    status        TEXT NOT NULL DEFAULT 'unknown'
        CHECK (status IN ('online','offline','unknown')),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (nvr_device_id, channel_no)
);

CREATE INDEX idx_nvr_channels_nvr ON nvr_channels (nvr_device_id);
