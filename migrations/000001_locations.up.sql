-- HIMS Phase 0: the location tree.
--
-- One typed, self-referential table models the whole hierarchy:
--   Hotel Group -> Hotel -> Building -> Floor -> Area -> Room -> Rack
-- A single table (rather than one per level) keeps the tree flexible: a
-- property may skip levels (a small hotel with no "area") without schema
-- gymnastics, and the path/ancestry queries are uniform.
--
-- `kind` constrains what a node represents; `parent_id` builds the tree.
-- The root (a hotel group) has parent_id NULL. Application logic enforces
-- sensible parent/child kind pairs (e.g. a rack's parent is a room/area).

CREATE TABLE locations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id   UUID REFERENCES locations(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL
        CHECK (kind IN ('group','hotel','building','floor','area','room','rack')),
    name        TEXT NOT NULL,
    -- Optional short code (e.g. hotel "CHV", building "B1") for compact
    -- display + operator-typed references.
    code        TEXT,
    -- Free-form attributes (GPS, address, rack U-height, etc.) without
    -- schema churn.
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Sibling names are unique under one parent. Two NULL parents (two
    -- roots) are allowed by the partial unique index below instead.
    UNIQUE (parent_id, name)
);

-- Root nodes (groups) have NULL parent; enforce unique root names since the
-- table UNIQUE(parent_id,name) treats NULLs as distinct.
CREATE UNIQUE INDEX idx_locations_root_name
    ON locations (name) WHERE parent_id IS NULL;

CREATE INDEX idx_locations_parent ON locations (parent_id);
CREATE INDEX idx_locations_kind ON locations (kind);
