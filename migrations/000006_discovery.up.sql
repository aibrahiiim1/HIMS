-- HIMS Phase 1: discovery jobs + results.
--
-- A discovery job tracks one scan-run (subnet, manual IP, hotel sweep, …).
-- Results are per-host rows the pipeline writes as it probes each IP.

CREATE TABLE discovery_jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Trigger scope: exactly one of location/subnet/cidr is set.
    location_id  UUID REFERENCES locations(id) ON DELETE SET NULL,
    subnet_id    UUID REFERENCES subnets(id) ON DELETE SET NULL,
    -- For ad-hoc single-IP or range discovery without a stored subnet.
    scope_cidr   CIDR,
    status       TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','completed','failed','cancelled')),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    host_count   INTEGER NOT NULL DEFAULT 0,
    found_count  INTEGER NOT NULL DEFAULT 0,
    error        TEXT,
    metadata     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_discovery_jobs_status ON discovery_jobs (status);
CREATE INDEX idx_discovery_jobs_location ON discovery_jobs (location_id);

CREATE TABLE discovery_results (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id      UUID NOT NULL REFERENCES discovery_jobs(id) ON DELETE CASCADE,
    ip          INET NOT NULL,
    -- Outcomes: alive / classified / enrolled / skipped / failed.
    outcome     TEXT NOT NULL DEFAULT 'pending'
        CHECK (outcome IN ('pending','alive','classified','enrolled','skipped','failed')),
    device_id   UUID REFERENCES devices(id) ON DELETE SET NULL,
    driver      TEXT,
    category    TEXT,
    -- Light probe evidence serialized for the pipeline's downstream stages.
    probe_data  JSONB NOT NULL DEFAULT '{}'::jsonb,
    error       TEXT,
    probed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_discovery_results_job ON discovery_results (job_id);
CREATE INDEX idx_discovery_results_ip ON discovery_results (ip);
