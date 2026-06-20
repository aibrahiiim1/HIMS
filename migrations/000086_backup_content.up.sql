-- Store the actual config-snapshot bytes with the backup run so a past backup can be re-downloaded
-- and individually deleted (and so a pre-reset auto-backup is recoverable). Nullable: external
-- pg_dump records have no in-DB content.
ALTER TABLE backup_runs ADD COLUMN content BYTEA;
