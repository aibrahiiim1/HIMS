ALTER TABLE credentials DROP COLUMN IF EXISTS needs_secret_reentry;
DROP TABLE IF EXISTS encryption_metadata;
