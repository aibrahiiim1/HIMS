-- HIMS Phase 10: MIB upload engine. An uploaded MIB is parsed into an OID
-- library (mib_objects); operators then bind an OID to a metric/template
-- (oid_mappings) so a vendor's MIB becomes usable monitoring vocabulary.
-- Upload alone ≠ understanding — the value is the mapping + binding.

CREATE TABLE mib_files (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    object_count  INT NOT NULL DEFAULT 0,
    unresolved    INT NOT NULL DEFAULT 0,
    uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mib_objects (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mib_file_id UUID NOT NULL REFERENCES mib_files(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    oid         TEXT NOT NULL,
    syntax      TEXT,
    kind        TEXT NOT NULL DEFAULT 'object',
    unresolved  BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX idx_mib_objects_file ON mib_objects (mib_file_id);
CREATE INDEX idx_mib_objects_oid ON mib_objects (oid);

-- Operator-curated bindings: an OID → a named metric on a template/vendor.
CREATE TABLE oid_mappings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    oid         TEXT NOT NULL,
    label       TEXT NOT NULL,
    metric_key  TEXT,
    vendor      TEXT,
    template    TEXT,
    notes       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (oid, metric_key)
);
