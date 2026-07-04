-- Comprehensive QNAP NAS inventory: iSCSI LUNs + targets, RAID storage pools, and
-- cooling fans, plus per-volume RAID status/pool. All from the QNAP NAS-MIB (24681)
-- and QTS-5 enterprise MIB (55062); real device evidence only.

-- Richer logical volumes: the QNAP volume table exposes the volume's RAID pool and a
-- Ready/Degraded status the HOST-RESOURCES mount view lacks.
ALTER TABLE nas_volumes ADD COLUMN status TEXT;
ALTER TABLE nas_volumes ADD COLUMN pool TEXT;

-- iSCSI LUNs and targets (one table, kind = 'lun' | 'target').
CREATE TABLE nas_iscsi (
    device_id      UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,   -- lun | target
    idx            INTEGER NOT NULL,
    name           TEXT NOT NULL,
    capacity_bytes BIGINT,          -- LUNs
    status         TEXT,
    iqn            TEXT,            -- targets
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, kind, idx)
);

-- RAID storage pools (the physical-disk aggregation the volumes sit on).
CREATE TABLE nas_pools (
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    idx          INTEGER NOT NULL,
    name         TEXT,
    raid_type    TEXT,            -- e.g. "RAID 5"
    raw_bytes    BIGINT,
    status       TEXT,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, idx)
);

-- Cooling fans (RPM + name).
CREATE TABLE nas_fans (
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    idx          INTEGER NOT NULL,
    name         TEXT NOT NULL,
    rpm          INTEGER,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, idx)
);

CREATE INDEX idx_nas_iscsi_device ON nas_iscsi (device_id);
CREATE INDEX idx_nas_pools_device ON nas_pools (device_id);
CREATE INDEX idx_nas_fans_device ON nas_fans (device_id);
