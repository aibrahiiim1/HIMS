-- Live Discovery event stream (v2). Per-stage scan events for the live visual
-- board (SSE) and for post-completion playback. The final discovery_results rows
-- remain the source of truth; these events are the play-by-play that produced them.
CREATE TABLE discovery_job_events (
    seq         BIGSERIAL PRIMARY KEY,
    job_id      UUID NOT NULL REFERENCES discovery_jobs(id) ON DELETE CASCADE,
    ip          INET,
    device_id   UUID REFERENCES devices(id) ON DELETE SET NULL,
    stage       TEXT NOT NULL,            -- target_probe_started, snmp_success, credential_bound, ...
    protocol    TEXT NOT NULL DEFAULT '', -- snmp | ssh | winrm | onvif | wmi | ...
    status      TEXT NOT NULL DEFAULT '', -- started | success | failed
    message     TEXT NOT NULL DEFAULT '',
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_discovery_job_events_job ON discovery_job_events (job_id, seq);
