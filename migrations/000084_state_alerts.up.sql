-- Part 2: state-based alerting. Rules that evaluate device/system STATE (not a monitoring
-- check's status), so their alerts have no check_id. Dedup is by (rule_id, fingerprint) where
-- the fingerprint encodes the condition's identity (device/agent/datastore), so a flapping
-- condition opens exactly one alert and auto-resolves when it clears.

ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS condition TEXT NOT NULL DEFAULT 'check'
        CHECK (condition IN ('check','collection_stale','agent_offline','virt_collection','datastore_low')),
    ADD COLUMN IF NOT EXISTS warn_threshold INT,   -- hours stale (collection/virt), minutes (agent), %free (datastore)
    ADD COLUMN IF NOT EXISTS crit_threshold INT;

-- State alerts may not target a device (e.g. an offline relay agent is site-scoped).
ALTER TABLE alerts ALTER COLUMN device_id DROP NOT NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS fingerprint TEXT NOT NULL DEFAULT '';

-- One un-resolved STATE alert per (rule, fingerprint) — the dedup model for check_id-less alerts.
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_state_one_open ON alerts (rule_id, fingerprint)
    WHERE check_id IS NULL AND status <> 'resolved';
