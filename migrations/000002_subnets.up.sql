-- HIMS Phase 0: subnets.
--
-- A subnet is the unit of BOTH discovery scope and credential scope. It is
-- pinned to a location (typically a hotel or building) so discovery can be
-- run "for this hotel" and credentials resolved by where the device lives.
--
-- cidr is the network in CIDR form (e.g. 172.21.96.0/24). The credential
-- resolver matches a device's IP against these prefixes to find candidate
-- credential groups (via credential_bindings, migration 000003).

CREATE TABLE subnets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    cidr        CIDR NOT NULL,
    name        TEXT,
    -- Optional VLAN id this subnet maps to, for topology correlation.
    vlan_id     INTEGER,
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (location_id, cidr)
);

-- Resolver hot path: "which subnet(s) contain this IP" uses the cidr index;
-- and "subnets for this location" uses location_id.
CREATE INDEX idx_subnets_location ON subnets (location_id);
CREATE INDEX idx_subnets_cidr ON subnets USING gist (cidr inet_ops);
