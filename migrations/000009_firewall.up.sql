-- HIMS Phase 4: FortiGate/firewall current-state inventory.
--
-- Dedicated per-device tables for firewall current state (HA, VPN, license,
-- per-member HA stats). CPU/RAM/disk/session snapshots live in device_facts.
-- Same source-scoped upsert+prune pattern as the network-inventory tables.
-- Design carried (validated) from the NIMS FortiGate work.

-- One row per firewall: HA summary + session-count snapshot.
CREATE TABLE firewall_status (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    ha_mode           TEXT NOT NULL DEFAULT 'unknown',  -- standalone|active-active|active-passive|unknown
    ha_group_name     TEXT,
    ha_member_count   INTEGER NOT NULL DEFAULT 0,
    session_count     BIGINT,
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id)
);
CREATE INDEX idx_firewall_status_device_source ON firewall_status (device_id, collection_source);

-- N IPsec phase-2 tunnels per firewall. Identity (device, tunnel_name);
-- in/out octets are Counter64 on the wire (validated lesson).
CREATE TABLE firewall_vpn_tunnels (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    tunnel_name       TEXT NOT NULL,
    p1_name           TEXT,
    remote_gw         INET,
    status            TEXT NOT NULL DEFAULT 'down',     -- up|down
    in_octets         BIGINT,
    out_octets        BIGINT,
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, tunnel_name)
);
CREATE INDEX idx_firewall_vpn_tunnels_device_source ON firewall_vpn_tunnels (device_id, collection_source);

-- N HA cluster members per firewall (fgHaStatsTable). Standalone reports 1.
CREATE TABLE firewall_ha_members (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    serial            TEXT NOT NULL,
    hostname          TEXT,
    cpu_pct           INTEGER,
    mem_pct           INTEGER,
    session_count     BIGINT,
    sync_status       TEXT NOT NULL DEFAULT 'unknown',  -- synchronized|unsynchronized|unknown
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, serial)
);
CREATE INDEX idx_firewall_ha_members_device_source ON firewall_ha_members (device_id, collection_source);

-- N FortiGuard/support contracts per firewall (fgLicContractTable).
CREATE TABLE firewall_licenses (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    contract          TEXT NOT NULL,
    expiry            TEXT,   -- firewall's own DisplayString; not parsed to a date
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, contract)
);
CREATE INDEX idx_firewall_licenses_device_source ON firewall_licenses (device_id, collection_source);
