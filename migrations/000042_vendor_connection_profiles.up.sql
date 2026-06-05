-- Vendor Connection Profiles.
-- Some device classes need more than a username/password to onboard — a target
-- URL plus vendor-specific connection parameters (vCenter URL + insecure TLS;
-- UniFi site / Omada controller-id / Ruckus apiBase; CUCM AXL schema version).
-- A profile pairs a stored credential with that connection config and (optionally)
-- a site/location or a specific device, so the scan can actually authenticate and
-- collect these integrations instead of leaving them config-gated.
--
-- SECURITY: the secret stays in the `credentials` table (encrypted). This table
-- holds only the non-secret connection config + test/collection outcome metadata.

CREATE TABLE vendor_connection_profiles (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    -- vmware | cctv | wireless_unifi | wireless_aruba | wireless_ruckus |
    -- wireless_extreme | wireless_omada | cucm | alcatel
    vendor_type   TEXT NOT NULL,
    target_url    TEXT NOT NULL DEFAULT '',
    credential_id UUID REFERENCES credentials(id) ON DELETE SET NULL,
    -- One of (or neither) — bind the profile to a site/location or a device.
    location_id   UUID REFERENCES locations(id) ON DELETE SET NULL,
    device_id     UUID REFERENCES devices(id) ON DELETE SET NULL,
    -- Vendor connection params: {site, controller_id, api_base, version, insecure}.
    config        JSONB NOT NULL DEFAULT '{}',
    enabled       BOOLEAN NOT NULL DEFAULT true,

    last_test_at        TIMESTAMPTZ,
    last_test_ok        BOOLEAN,
    last_test_detail    TEXT NOT NULL DEFAULT '',
    last_collection_at  TIMESTAMPTZ,
    last_collection_detail TEXT NOT NULL DEFAULT '',
    -- unconfigured | untested | ok | failed
    status        TEXT NOT NULL DEFAULT 'untested',

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_vcp_vendor ON vendor_connection_profiles (vendor_type) WHERE enabled;
CREATE INDEX idx_vcp_location ON vendor_connection_profiles (location_id) WHERE enabled;
CREATE INDEX idx_vcp_device ON vendor_connection_profiles (device_id) WHERE enabled;
