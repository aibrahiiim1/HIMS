-- Stage 2: durable rich virtualization inventory (ESXi + Hyper-V). Replaces fact-only
-- storage so host detail pages have real storage/network/hardware/VM-detail data.

-- Host datastores (ESXi) / storage volumes.
CREATE TABLE vh_datastores (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    type           TEXT,
    capacity_bytes BIGINT,
    free_bytes     BIGINT,
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_device_id, name)
);
CREATE INDEX idx_vh_datastores_host ON vh_datastores (host_device_id);

-- Host virtual networks: vSwitches + port groups (ESXi), virtual switches (Hyper-V).
CREATE TABLE vh_networks (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL, -- vswitch | portgroup
    name           TEXT NOT NULL,
    vlan           INT,
    uplinks        TEXT,          -- comma-joined pnic names (vswitch)
    switch_name    TEXT,          -- parent vswitch (portgroup) / switch type (hyperv)
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_device_id, kind, name)
);
CREATE INDEX idx_vh_networks_host ON vh_networks (host_device_id);

-- Host physical NICs: ESXi vmnics / Hyper-V host pNICs.
CREATE TABLE vh_host_nics (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_device_id  UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    mac             TEXT,
    link_speed_mbps INT,
    link_up         BOOLEAN,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_device_id, name)
);
CREATE INDEX idx_vh_host_nics_host ON vh_host_nics (host_device_id);

-- Per-VM virtual disks (vmdk / vhd/vhdx).
CREATE TABLE vm_disks (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id          UUID NOT NULL REFERENCES virtual_machines(id) ON DELETE CASCADE,
    label          TEXT NOT NULL,
    path           TEXT,
    datastore      TEXT,
    capacity_bytes BIGINT,
    used_bytes     BIGINT,
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vm_id, label)
);
CREATE INDEX idx_vm_disks_vm ON vm_disks (vm_id);

-- Per-VM virtual NICs.
CREATE TABLE vm_nics (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id         UUID NOT NULL REFERENCES virtual_machines(id) ON DELETE CASCADE,
    mac           TEXT NOT NULL,
    network       TEXT,           -- portgroup / vswitch name
    ip_addresses  TEXT,
    connected     BOOLEAN,
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vm_id, mac)
);
CREATE INDEX idx_vm_nics_vm ON vm_nics (vm_id);

-- Collection health per collector per host (honest per-collector status).
CREATE TABLE vh_collection_health (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    collector    TEXT NOT NULL, -- vsphere | hyperv
    status       TEXT NOT NULL, -- ok | partial | failed
    detail       TEXT,
    vm_count     INT,
    collected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, collector)
);

-- Richer VM attributes (best-effort per platform).
ALTER TABLE virtual_machines
    ADD COLUMN IF NOT EXISTS tools_state          TEXT,
    ADD COLUMN IF NOT EXISTS generation           TEXT,
    ADD COLUMN IF NOT EXISTS uptime_seconds        BIGINT,
    ADD COLUMN IF NOT EXISTS mem_used_mb           INT,
    ADD COLUMN IF NOT EXISTS datastore             TEXT,
    ADD COLUMN IF NOT EXISTS integration_services  TEXT;
