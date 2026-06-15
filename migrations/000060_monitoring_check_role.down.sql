DROP INDEX IF EXISTS idx_monitoring_checks_device_role;
ALTER TABLE monitoring_checks DROP COLUMN IF EXISTS role;
