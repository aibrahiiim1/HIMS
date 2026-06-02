-- HIMS Phase 1: normalized network-inventory tables.
--
-- These are the generic tables shared by ALL switch drivers (Aruba, Cisco,
-- Huawei, Extreme, …). Vendor specifics stay in device_facts. Each table
-- carries collection_source so CLI-sourced rows and SNMP-sourced rows prune
-- independently (the source-scoping discipline proven in NIMS).

-- ---- Interfaces (IF-MIB) ------------------------------------------------
CREATE TABLE interfaces (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    if_index          INTEGER NOT NULL,
    if_name           TEXT,
    if_descr          TEXT,
    if_alias          TEXT,
    if_type           INTEGER,       -- IANAifType
    mac               TEXT,          -- lowercase colon-hex
    speed_mbps        INTEGER,
    admin_status      SMALLINT,      -- 1=up 2=down 3=testing
    oper_status       SMALLINT,      -- 1=up 2=down …
    -- Derived port role: access / trunk / uplink / edge / disabled / unknown.
    port_role         TEXT NOT NULL DEFAULT 'unknown',
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, if_index)
);

CREATE INDEX idx_interfaces_device_source ON interfaces (device_id, collection_source);
CREATE INDEX idx_interfaces_mac ON interfaces (mac);

-- ---- VLANs (Q-BRIDGE-MIB) -----------------------------------------------
CREATE TABLE vlans (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    vlan_id           INTEGER NOT NULL,
    name              TEXT,
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, vlan_id)
);

CREATE INDEX idx_vlans_device ON vlans (device_id);

-- Per-port VLAN membership: which VLANs a port carries, tagged or untagged.
CREATE TABLE port_vlans (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    if_index          INTEGER NOT NULL,
    vlan_id           INTEGER NOT NULL,
    tagged            BOOLEAN NOT NULL DEFAULT true,
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, if_index, vlan_id)
);

CREATE INDEX idx_port_vlans_device ON port_vlans (device_id);

-- ---- MAC address table (FDB) --------------------------------------------
CREATE TABLE mac_addresses (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    mac               TEXT NOT NULL,
    vlan_id           INTEGER NOT NULL DEFAULT 0,
    if_index          INTEGER,
    -- fdb_status mirrors Q-BRIDGE dot1qTpFdbStatus.
    fdb_status        SMALLINT NOT NULL DEFAULT 3,
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, mac, vlan_id)
);

CREATE INDEX idx_mac_addresses_mac ON mac_addresses (mac);
CREATE INDEX idx_mac_addresses_device ON mac_addresses (device_id);

-- ---- ARP table (IP-MIB) -------------------------------------------------
CREATE TABLE arp_entries (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    ip_address        INET NOT NULL,
    mac               TEXT NOT NULL,
    if_index          INTEGER,
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, ip_address, mac)
);

CREATE INDEX idx_arp_entries_ip ON arp_entries (ip_address);
CREATE INDEX idx_arp_entries_mac ON arp_entries (mac);
CREATE INDEX idx_arp_entries_device ON arp_entries (device_id);

-- ---- LLDP / CDP neighbors -----------------------------------------------
CREATE TABLE neighbors (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    local_if_index    INTEGER,
    local_if_name     TEXT,
    -- Remote identity from LLDP/CDP.
    rem_chassis_id    TEXT,          -- hex-encoded MAC or IP
    rem_sys_name      TEXT,
    rem_sys_desc      TEXT,
    rem_port_id       TEXT,
    rem_port_desc     TEXT,
    rem_mgmt_ip       INET,
    -- Protocol that observed this neighbor.
    protocol          TEXT NOT NULL DEFAULT 'lldp'
        CHECK (protocol IN ('lldp','cdp')),
    collection_source TEXT NOT NULL DEFAULT 'snmp'
        CHECK (collection_source IN ('snmp','cli','api')),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, local_if_index, rem_chassis_id)
);

CREATE INDEX idx_neighbors_device ON neighbors (device_id, collection_source);
CREATE INDEX idx_neighbors_rem_sys_name ON neighbors (rem_sys_name);
CREATE INDEX idx_neighbors_rem_mgmt_ip ON neighbors (rem_mgmt_ip);

-- ---- Topology links (computed) ------------------------------------------
-- Computed by the topology engine from LLDP/CDP + MAC + ARP. One row per
-- directed link (a → b). The engine writes both (a→b) and (b→a) so
-- queries can go either direction. local_if_index + remote_if_index are
-- populated when the source is LLDP/CDP (they're in the neighbor message).

CREATE TABLE topology_links (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    local_device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    local_if_index     INTEGER,
    local_if_name      TEXT,
    remote_device_id   UUID REFERENCES devices(id) ON DELETE CASCADE,
    -- When the remote is not yet in the DB, store its identifier so the
    -- topology UI can still draw a stub node.
    remote_ip          INET,
    remote_sys_name    TEXT,
    link_source        TEXT NOT NULL DEFAULT 'lldp'
        CHECK (link_source IN ('lldp','cdp','mac','arp')),
    last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (local_device_id, local_if_index, remote_device_id)
);

CREATE INDEX idx_topology_links_local ON topology_links (local_device_id);
CREATE INDEX idx_topology_links_remote ON topology_links (remote_device_id);
