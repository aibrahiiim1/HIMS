-- FP-ext: make the vendor-fingerprint library a complete, operator-managed feature.
--
-- 1. Allow 'sysname' as a fingerprint kind (match against SNMP sysName.0). The
--    engine already supports KindSysName; this lets operators create such rules.
-- 2. model    — an optional explicit product model a fingerprint stamps (e.g.
--               "VE6120 Medium") when the sysDescr alone wouldn't reveal it.
-- 3. priority  — operator-defined ordering; lower runs first. Among equal-confidence
--               matches the lowest priority (then user-over-builtin) wins.
-- 4. source    — provenance: 'builtin' (seeded from the shipped catalog) vs 'user'
--               (created/edited/imported by an operator). User rules outrank builtin
--               ones at equal confidence so a local override always takes effect.
-- 5. updated_at — last-edit timestamp for the management UI.

ALTER TABLE vendor_fingerprints DROP CONSTRAINT IF EXISTS vendor_fingerprints_kind_check;
ALTER TABLE vendor_fingerprints
    ADD CONSTRAINT vendor_fingerprints_kind_check
    CHECK (kind IN ('oid','service','sysname','port','http','ssh'));

ALTER TABLE vendor_fingerprints
    ADD COLUMN IF NOT EXISTS model      TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS priority   INTEGER     NOT NULL DEFAULT 100,
    ADD COLUMN IF NOT EXISTS source     TEXT        NOT NULL DEFAULT 'user'
        CHECK (source IN ('builtin','user')),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Existing rows were seeded from the shipped catalog (the "Import standard
-- library" button) — mark them builtin so user-created rows can outrank them.
UPDATE vendor_fingerprints SET source = 'builtin' WHERE source = 'user';

CREATE INDEX IF NOT EXISTS idx_vendor_fingerprints_priority ON vendor_fingerprints (priority);

-- Enforce one rule per (kind, pattern) so import is idempotent (ON CONFLICT).
-- Collapse any pre-existing accidental duplicates first, keeping the newest row.
DELETE FROM vendor_fingerprints a USING vendor_fingerprints b
    WHERE a.ctid < b.ctid AND a.kind = b.kind AND a.pattern = b.pattern;
CREATE UNIQUE INDEX IF NOT EXISTS uq_vendor_fingerprints_kind_pattern
    ON vendor_fingerprints (kind, pattern);
