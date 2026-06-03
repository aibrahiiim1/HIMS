-- HIMS Phase 8: wireless. wlan_controller_info holds controller summary;
-- access_points is the AP inventory under a controller. Populated by the
-- vendor-REST transport (deferred); tables + read paths ship now.

CREATE TABLE wlan_controller_info (
    device_id     UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    vendor        TEXT,
    version       TEXT,
    ap_count      INT NOT NULL DEFAULT 0,
    client_count  INT NOT NULL DEFAULT 0,
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE access_points (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    controller_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    mac                  TEXT,
    model                TEXT,
    ip                   INET,
    status               TEXT NOT NULL DEFAULT 'unknown'
        CHECK (status IN ('online','offline','unknown')),
    client_count         INT NOT NULL DEFAULT 0,
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (controller_device_id, name)
);

CREATE INDEX idx_access_points_controller ON access_points (controller_device_id);
