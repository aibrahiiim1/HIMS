-- Allow the automatic pre-reset backup kind.
ALTER TABLE backup_runs DROP CONSTRAINT IF EXISTS backup_runs_kind_check;
ALTER TABLE backup_runs ADD CONSTRAINT backup_runs_kind_check
  CHECK (kind = ANY (ARRAY['config_export'::text, 'external_pg_dump'::text, 'pre_reset'::text]));
