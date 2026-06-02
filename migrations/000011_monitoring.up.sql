-- HIMS Phase 6: Monitoring Engine.
--
-- Monitoring is DISTINCT from discovery (PLAN §6): discovery runs daily/weekly
-- and rewrites inventory; monitoring polls registered devices on a short
-- interval (default 60s) and records a time-series of reachability samples.
--
-- Phase 6 core ships TCP-reachability checks only — they need no credentials
-- and no new transport (a plain dial), so the engine is honest and runnable
-- everywhere. SNMP-metric checks (sysUpTime / CPU / RAM) need the
-- credential-decrypt path in the collector and land in Phase 6B; the schema
-- already carries `kind='snmp'` + `oid` so 6B is additive, not a migration.

CREATE TABLE monitoring_checks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id     UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL DEFAULT 'tcp'
        CHECK (kind IN ('tcp','snmp')),
    -- tcp: dial this port on the device primary IP. snmp (6B): poll `oid`.
    target_port   INT,
    oid           TEXT,
    interval_seconds INT NOT NULL DEFAULT 60
        CHECK (interval_seconds >= 10),
    -- down_threshold: consecutive failures before status flips up→down.
    -- 1 failure short of the threshold reports 'warning' (a transient blip).
    down_threshold INT NOT NULL DEFAULT 2 CHECK (down_threshold >= 1),
    enabled       BOOLEAN NOT NULL DEFAULT true,
    -- Last-run rollup (the live state; full history is in monitoring_samples).
    last_run_at   TIMESTAMPTZ,
    last_status   TEXT NOT NULL DEFAULT 'unknown'
        CHECK (last_status IN ('up','down','warning','unknown')),
    last_latency_ms DOUBLE PRECISION,
    consecutive_failures INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One check per (device, kind, port): re-registering is idempotent.
    UNIQUE (device_id, kind, target_port)
);

CREATE INDEX idx_monitoring_checks_device ON monitoring_checks (device_id);
CREATE INDEX idx_monitoring_checks_due ON monitoring_checks (enabled, last_run_at);

-- Time-series of every poll. device_id is denormalized so per-device history
-- queries don't need a join. Promoted to a TimescaleDB hypertable when the
-- extension is present (see DO block below); a plain table otherwise.
CREATE TABLE monitoring_samples (
    time        TIMESTAMPTZ NOT NULL DEFAULT now(),
    check_id    UUID NOT NULL REFERENCES monitoring_checks(id) ON DELETE CASCADE,
    device_id   UUID NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('up','down','warning','unknown')),
    latency_ms  DOUBLE PRECISION,
    value_num   DOUBLE PRECISION,   -- reserved for 6B SNMP metric values
    error       TEXT
);

CREATE INDEX idx_monitoring_samples_check ON monitoring_samples (check_id, time DESC);
CREATE INDEX idx_monitoring_samples_device ON monitoring_samples (device_id, time DESC);

-- Best-effort hypertable: only if TimescaleDB is installed. Plain Postgres
-- keeps the table as-is (the indexes above still serve the time-range reads).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        PERFORM create_hypertable('monitoring_samples', 'time',
            chunk_time_interval => INTERVAL '7 days',
            if_not_exists => TRUE);
    END IF;
END $$;
