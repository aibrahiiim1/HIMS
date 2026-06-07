-- Wireless deep-collection receiving schema (Extreme XCC + any source). Extends
-- the Phase-8 controller/AP tables with provenance + identity, and adds SSID,
-- client, radio and event tables so a real collector (REST/API or future SNMP)
-- can populate the Wireless Controller detail page. `source` records HOW each
-- row was obtained (snmp_baseline | extreme_xcc_api | cloud_xiq | unifi | …) so
-- the UI can be honest about coverage; `collected_at` enables staleness checks.

-- Controller summary: provenance + richer identity.
ALTER TABLE wlan_controller_info
    ADD COLUMN IF NOT EXISTS source          TEXT NOT NULL DEFAULT 'snmp_baseline',
    ADD COLUMN IF NOT EXISTS profile_id       UUID REFERENCES vendor_connection_profiles(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS controller_name  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS model            TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS serial           TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ssid_count       INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS collected_at     TIMESTAMPTZ NOT NULL DEFAULT now();

-- Access points: serial / firmware / band + provenance.
ALTER TABLE access_points
    ADD COLUMN IF NOT EXISTS serial        TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS firmware      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS band          TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source        TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS collected_at  TIMESTAMPTZ NOT NULL DEFAULT now();

-- SSIDs / WLAN services advertised by the controller.
CREATE TABLE IF NOT EXISTS wireless_ssids (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    controller_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'unknown', -- enabled | disabled | unknown
    security             TEXT NOT NULL DEFAULT '',         -- wpa2-psk | wpa2-enterprise | open | …
    band                 TEXT NOT NULL DEFAULT '',          -- 2.4GHz | 5GHz | 6GHz | dual
    vlan                 TEXT NOT NULL DEFAULT '',
    client_count         INT  NOT NULL DEFAULT 0,
    source               TEXT NOT NULL DEFAULT '',
    collected_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (controller_device_id, name)
);
CREATE INDEX IF NOT EXISTS idx_wireless_ssids_controller ON wireless_ssids (controller_device_id);

-- Associated wireless clients (stations).
CREATE TABLE IF NOT EXISTS wireless_clients (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    controller_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    mac                  TEXT NOT NULL,
    ip                   TEXT NOT NULL DEFAULT '',
    hostname             TEXT NOT NULL DEFAULT '',
    ap_name              TEXT NOT NULL DEFAULT '',
    ssid                 TEXT NOT NULL DEFAULT '',
    rssi                 INT,
    band                 TEXT NOT NULL DEFAULT '',
    source               TEXT NOT NULL DEFAULT '',
    collected_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (controller_device_id, mac)
);
CREATE INDEX IF NOT EXISTS idx_wireless_clients_controller ON wireless_clients (controller_device_id);

-- Per-AP radio status (channel / power / band).
CREATE TABLE IF NOT EXISTS wireless_radio_status (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    controller_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    ap_name              TEXT NOT NULL,
    radio                TEXT NOT NULL DEFAULT '',  -- radio index / name (e.g. wifi0)
    band                 TEXT NOT NULL DEFAULT '',
    channel              INT,
    power_dbm            INT,
    client_count         INT NOT NULL DEFAULT 0,
    source               TEXT NOT NULL DEFAULT '',
    collected_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (controller_device_id, ap_name, radio)
);
CREATE INDEX IF NOT EXISTS idx_wireless_radio_controller ON wireless_radio_status (controller_device_id);

-- Controller events / alarms (append-only; capped read).
CREATE TABLE IF NOT EXISTS wireless_events (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    controller_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    severity             TEXT NOT NULL DEFAULT 'info', -- info | warning | critical
    category             TEXT NOT NULL DEFAULT '',
    message              TEXT NOT NULL DEFAULT '',
    source               TEXT NOT NULL DEFAULT '',
    collected_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_wireless_events_controller ON wireless_events (controller_device_id, at DESC);
