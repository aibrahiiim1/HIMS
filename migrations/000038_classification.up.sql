-- Add-on: richer device classification.
-- Adds an OS family, a 0..100 confidence score, an evidence trail explaining WHY
-- a device was classified, and a manual-override lock so an operator-set
-- classification is never silently overwritten by a later auto-classification.
-- The subtype (windows_server / linux_server / domain_controller / nvr-vs-camera
-- nuance) continues to live in the existing device_class column — we extend the
-- existing encoding rather than add a parallel column.

ALTER TABLE devices
    ADD COLUMN os_family TEXT NOT NULL DEFAULT '',
    ADD COLUMN confidence_score SMALLINT,
    ADD COLUMN classification_evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN classification_locked BOOLEAN NOT NULL DEFAULT false;

-- confidence_score is a percentage when known; NULL means "not yet scored".
ALTER TABLE devices ADD CONSTRAINT devices_confidence_score_range
    CHECK (confidence_score IS NULL OR (confidence_score BETWEEN 0 AND 100));

-- classification_evidence is always a JSON array of evidence objects (shape
-- enforced at the schema layer, consistent with other JSONB columns here).
ALTER TABLE devices ADD CONSTRAINT devices_classification_evidence_is_array
    CHECK (jsonb_typeof(classification_evidence) = 'array');

-- Filter inventory by OS family without scanning every row.
CREATE INDEX idx_devices_os_family ON devices (os_family) WHERE os_family <> '';
