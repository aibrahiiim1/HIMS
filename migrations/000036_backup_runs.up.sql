-- Backup & Restore (#25): a registry of backup runs. HIMS produces a portable
-- config snapshot (JSON, no raw secrets) in-process and records each run here;
-- operators also log external full-DB pg_dump backups so DR readiness reflects
-- real off-box backups. The encryption key must be backed up out-of-band (the
-- DR checklist enforces this) — encrypted secrets are useless without it.
CREATE TABLE backup_runs (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind       TEXT NOT NULL DEFAULT 'config_export'
        CHECK (kind IN ('config_export','external_pg_dump')),
    status     TEXT NOT NULL DEFAULT 'success' CHECK (status IN ('success','failed')),
    tables     INT NOT NULL DEFAULT 0,
    rows       INT NOT NULL DEFAULT 0,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    actor      TEXT NOT NULL DEFAULT 'operator',
    detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_backup_runs_at ON backup_runs (at DESC);
