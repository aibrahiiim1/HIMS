-- HIMS Relay Agent / Site Collector. One installable agent runs on a trusted
-- machine inside a site and collects from devices hard to reach directly from
-- the main HIMS API. Replaces the separate Native/WMI collector helpers with a
-- single agent model (the helper env-vars remain backward-compatible for now).
--
-- The agent authenticates with a per-agent bearer token (only its SHA-256 hash is
-- stored). It pulls jobs (NAT-friendly: no inbound path to the agent required),
-- executes collection locally, and posts structured results back.

CREATE TABLE IF NOT EXISTS relay_agents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    location_id    UUID REFERENCES locations(id) ON DELETE SET NULL,
    token_hash     TEXT NOT NULL,                          -- sha256(hex) of the agent token; the token itself is shown once
    hostname       TEXT NOT NULL DEFAULT '',
    ip             TEXT NOT NULL DEFAULT '',
    os             TEXT NOT NULL DEFAULT '',
    version        TEXT NOT NULL DEFAULT '',
    capabilities   JSONB NOT NULL DEFAULT '[]'::jsonb,     -- ["winrm","wmi","ssh","snmp","onvif","vsphere"]
    status         TEXT NOT NULL DEFAULT 'registered'      -- registered | online | offline | disabled
        CHECK (status IN ('registered','online','offline','disabled')),
    enabled        BOOLEAN NOT NULL DEFAULT TRUE,
    last_heartbeat TIMESTAMPTZ,
    last_error     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_relay_agents_location ON relay_agents (location_id);

CREATE TABLE IF NOT EXISTS agent_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id      UUID NOT NULL REFERENCES relay_agents(id) ON DELETE CASCADE,
    device_id     UUID REFERENCES devices(id) ON DELETE SET NULL,
    credential_id UUID REFERENCES credentials(id) ON DELETE SET NULL,
    kind          TEXT NOT NULL DEFAULT 'collect_os',      -- collect_os | test
    protocol      TEXT NOT NULL DEFAULT '',                -- winrm | wmi | ssh
    target        TEXT NOT NULL DEFAULT '',                -- host / IP
    status        TEXT NOT NULL DEFAULT 'queued'           -- queued | dispatched | done | failed
        CHECK (status IN ('queued','dispatched','done','failed')),
    request       JSONB NOT NULL DEFAULT '{}'::jsonb,
    result        JSONB,
    category      TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_agent_jobs_agent_status ON agent_jobs (agent_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_jobs_device ON agent_jobs (device_id);
