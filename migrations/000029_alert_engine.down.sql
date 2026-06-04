ALTER TABLE alert_rules DROP COLUMN IF EXISTS escalate_after_minutes;
ALTER TABLE alerts DROP COLUMN IF EXISTS escalated_at;
ALTER TABLE alerts DROP COLUMN IF EXISTS escalated;
ALTER TABLE alerts DROP COLUMN IF EXISTS acknowledged_by;
DROP TABLE IF EXISTS alert_events;
DROP TABLE IF EXISTS maintenance_windows;
