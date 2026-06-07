DROP INDEX IF EXISTS uq_vendor_fingerprints_kind_pattern;
DROP INDEX IF EXISTS idx_vendor_fingerprints_priority;

ALTER TABLE vendor_fingerprints
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS priority,
    DROP COLUMN IF EXISTS model;

ALTER TABLE vendor_fingerprints DROP CONSTRAINT IF EXISTS vendor_fingerprints_kind_check;
ALTER TABLE vendor_fingerprints
    ADD CONSTRAINT vendor_fingerprints_kind_check
    CHECK (kind IN ('oid','service','port','http','ssh'));
