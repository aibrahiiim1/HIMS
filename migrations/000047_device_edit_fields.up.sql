-- Operator-editable device fields surfaced by the shared Edit Device form.
-- classification_locked / confidence_score / classification_evidence already
-- exist (000038); these are the remaining manual-management attributes.
ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS subtype            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS notes              TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS criticality        TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS monitoring_enabled BOOLEAN NOT NULL DEFAULT true,
    -- Why the classification is locked (operator's manual reason), shown as the
    -- "manual override" provenance. Empty unless the operator locked it.
    ADD COLUMN IF NOT EXISTS manual_classification_reason TEXT NOT NULL DEFAULT '';

-- criticality is a small controlled vocabulary; keep it honest at the DB layer.
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_criticality_check;
ALTER TABLE devices ADD CONSTRAINT devices_criticality_check
    CHECK (criticality IN ('', 'low', 'normal', 'high', 'critical'));
