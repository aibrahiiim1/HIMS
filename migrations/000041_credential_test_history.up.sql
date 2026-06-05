-- Credential Test History.
-- Every credential↔device↔protocol test result is persisted so Management Access
-- Coverage, Inventory filters, Data Quality, Device Detail and the Credential
-- Testing page can show real, durable credential status instead of losing it when
-- the test screen closes.
--
-- SECURITY: secrets are NEVER stored here. Only outcome metadata —
-- credential id/kind, device id, protocol, success/category, a non-secret reason
-- string, latency, who/when. The `detail` column is a generic categorised reason
-- ("authentication rejected", "could not connect"), never a password/community.

-- One row per test invocation (a matrix of creds × devices).
CREATE TABLE credential_test_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    actor       TEXT NOT NULL DEFAULT '',
    pairs       INT  NOT NULL DEFAULT 0,
    successes   INT  NOT NULL DEFAULT 0,
    failures    INT  NOT NULL DEFAULT 0
);

-- One row per (credential, device, protocol) outcome within a run.
CREATE TABLE credential_test_results (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id        UUID NOT NULL REFERENCES credential_test_runs(id) ON DELETE CASCADE,
    device_id     UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    -- Keep history if the credential is later deleted (id nulled, name retained).
    credential_id UUID REFERENCES credentials(id) ON DELETE SET NULL,
    credential_name TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT '', -- credential kind at test time = precise protocol key
    protocol      TEXT NOT NULL DEFAULT '', -- probe protocol family (snmp|ssh|winrm|onvif|http)
    category      TEXT NOT NULL,            -- success|auth_failed|unreachable|unsupported|error
    success       BOOLEAN NOT NULL,
    detail        TEXT NOT NULL DEFAULT '', -- non-secret categorised reason
    latency_ms    BIGINT NOT NULL DEFAULT 0,
    tested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor         TEXT NOT NULL DEFAULT ''
);

-- Indexed discriminators (audit-volume-friendly) for "latest per device /
-- credential / protocol" lookups and the Inventory access filters.
CREATE INDEX idx_ctr_device_time ON credential_test_results (device_id, tested_at DESC);
CREATE INDEX idx_ctr_credential_time ON credential_test_results (credential_id, tested_at DESC);
CREATE INDEX idx_ctr_device_kind_time ON credential_test_results (device_id, kind, tested_at DESC);
CREATE INDEX idx_ctr_success ON credential_test_results (success);
CREATE INDEX idx_ctr_category ON credential_test_results (category);
CREATE INDEX idx_ctr_run ON credential_test_results (run_id);
