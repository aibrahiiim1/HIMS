DROP INDEX IF EXISTS idx_devices_is_virtual;
ALTER TABLE devices DROP COLUMN IF EXISTS is_virtual;
