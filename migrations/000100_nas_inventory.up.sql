-- NAS deep inventory (QNAP QTS today; extensible to other NAS vendors). nas_info is
-- the per-device summary (identity + live system health); nas_disks is the physical
-- disk bay table; nas_volumes is the logical storage-volume/pool table. Populated by
-- the QNAP SNMP collector (internal/nas). Network interfaces are NOT duplicated here —
-- they reuse the shared `interfaces` table (collection_source='snmp'), read back via
-- GET /devices/{id}/interfaces, so the NAS "Network" tab shares one source of truth.

CREATE TABLE nas_info (
    device_id         UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    vendor            TEXT,
    model             TEXT,
    firmware          TEXT,
    serial            TEXT,             -- SNMP does not expose a QNAP serial; QTS-API only (honest NULL)
    hostname          TEXT,
    cpu_pct           DOUBLE PRECISION, -- NULL when the device did not report CPU usage
    mem_total_bytes   BIGINT,
    mem_used_bytes    BIGINT,
    cpu_temp_c        INTEGER,          -- NULL when no CPU temperature sensor was read
    sys_temp_c        INTEGER,          -- NULL when no system temperature sensor was read
    uptime_seconds    BIGINT,
    disk_count        INTEGER NOT NULL DEFAULT 0,
    volume_count      INTEGER NOT NULL DEFAULT 0,
    health            TEXT,             -- OK | Warning | Critical | Unknown (rolled up from disks)
    collection_source TEXT NOT NULL DEFAULT 'snmp',
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One physical disk bay. slot is the vendor's disk index (1-based). Absent bays are
-- simply not present. capacity/temp/health come straight from the vendor MIB.
CREATE TABLE nas_disks (
    device_id       UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    slot            INTEGER NOT NULL,
    vendor          TEXT,
    model           TEXT,
    serial          TEXT,
    interface_type  TEXT,            -- SATA | SAS | NVMe | ...
    capacity_bytes  BIGINT,
    temp_c          INTEGER,
    health          TEXT,            -- Good | Warning | Error | ... (vendor string, normalized best-effort)
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, slot)
);

-- One logical storage volume / pool mount (the operator-facing data volumes, e.g.
-- /share/CACHEDEV1_DATA, /mnt/pool1). System/OS mounts are filtered out by the collector.
CREATE TABLE nas_volumes (
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    idx          INTEGER NOT NULL,
    name         TEXT NOT NULL,
    fs_type      TEXT,               -- disk | ... (hrStorageType-derived)
    total_bytes  BIGINT,
    used_bytes   BIGINT,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, idx)
);

CREATE INDEX idx_nas_disks_device ON nas_disks (device_id);
CREATE INDEX idx_nas_volumes_device ON nas_volumes (device_id);
