-- HIMS: operator-managed value lists for the Inventory classification dropdowns
-- (Class and VLAN). Managed in Settings; referenced (by value) on devices.
-- Location uses the locations tree (location_id), not a lookup.

CREATE TABLE lookups (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT NOT NULL CHECK (kind IN ('class','vlan')),
    value      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, value)
);

CREATE INDEX idx_lookups_kind ON lookups (kind);
