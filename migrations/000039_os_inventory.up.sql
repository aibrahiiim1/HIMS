-- Deep OS Inventory (Windows via WinRM/PowerShell, Linux via SSH).
-- A 1:1 summary row per device plus 1:N typed collections. Every table records
-- HOW + WHEN it was collected (collection_method / collection_source +
-- collected_at / last_seen_at) so the UI can honestly show "Not collected yet"
-- and so a re-collection prunes rows no longer present (the established
-- last_seen_at prune-on-poll pattern). Nothing here is populated until an
-- authenticated collection actually runs.

-- 1:1 OS + identity + hardware summary.
CREATE TABLE os_inventory (
    device_id         UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    collection_method TEXT NOT NULL CHECK (collection_method IN ('winrm','ssh','snmp')),
    collected_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- identity
    hostname              TEXT,
    fqdn                  TEXT,
    domain                TEXT,
    workgroup             TEXT,
    logged_on_user        TEXT,
    ad_distinguished_name TEXT,
    ad_ou_path            TEXT,

    -- operating system
    os_caption     TEXT,
    os_version     TEXT,
    os_build       TEXT,
    os_edition     TEXT,
    os_arch        TEXT,
    kernel         TEXT,          -- linux
    install_date   TIMESTAMPTZ,
    last_boot      TIMESTAMPTZ,
    uptime_seconds BIGINT,
    timezone       TEXT,

    -- hardware
    manufacturer    TEXT,
    model           TEXT,
    serial          TEXT,
    asset_tag       TEXT,
    bios_version    TEXT,
    bios_date       TEXT,
    cpu_model       TEXT,
    cpu_sockets     INT,
    cpu_cores       INT,
    ram_total_bytes BIGINT,
    ram_slots       INT,
    swap_total_bytes BIGINT,

    -- event-log summary (Windows; NULL elsewhere). Summary counts only — we do
    -- not ingest full event logs in this version.
    events_critical_24h INT,
    events_error_24h    INT,
    events_warning_24h  INT,
    last_critical_event TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reusable source CHECK for the 1:N collections.
-- ('winrm' = Windows PowerShell, 'ssh' = Linux shell, 'snmp' = host-resources fallback)

CREATE TABLE os_disks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,   -- drive letter / mount point / device name (identity within device)
    model             TEXT,
    serial            TEXT,
    filesystem        TEXT,
    size_bytes        BIGINT,
    total_bytes       BIGINT,
    free_bytes        BIGINT,
    health            TEXT,
    collection_source TEXT NOT NULL DEFAULT 'winrm' CHECK (collection_source IN ('winrm','ssh','snmp')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, name)
);
CREATE INDEX idx_os_disks_device ON os_disks (device_id);

CREATE TABLE os_nics (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    mac               TEXT,
    ip_addresses      TEXT,   -- comma-separated
    gateway           TEXT,
    dns_servers       TEXT,   -- comma-separated
    dhcp_enabled      BOOLEAN,
    link_speed_mbps   BIGINT,
    collection_source TEXT NOT NULL DEFAULT 'winrm' CHECK (collection_source IN ('winrm','ssh','snmp')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, name)
);
CREATE INDEX idx_os_nics_device ON os_nics (device_id);

CREATE TABLE os_services (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    display_name      TEXT,
    status            TEXT,   -- running | stopped | ...
    start_type        TEXT,   -- auto | manual | disabled | enabled
    account           TEXT,
    description       TEXT,
    collection_source TEXT NOT NULL DEFAULT 'winrm' CHECK (collection_source IN ('winrm','ssh','snmp')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, name)
);
CREATE INDEX idx_os_services_device ON os_services (device_id);

CREATE TABLE os_processes (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    pid               INT NOT NULL,
    name              TEXT NOT NULL,
    cpu_pct           DOUBLE PRECISION,
    mem_bytes         BIGINT,
    start_time        TIMESTAMPTZ,
    collection_source TEXT NOT NULL DEFAULT 'winrm' CHECK (collection_source IN ('winrm','ssh','snmp')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, pid)
);
CREATE INDEX idx_os_processes_device ON os_processes (device_id);

CREATE TABLE os_software (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    version           TEXT NOT NULL DEFAULT '',
    publisher         TEXT,
    arch              TEXT,
    install_date      TEXT,
    collection_source TEXT NOT NULL DEFAULT 'winrm' CHECK (collection_source IN ('winrm','ssh','snmp')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, name, version)
);
CREATE INDEX idx_os_software_device ON os_software (device_id);
