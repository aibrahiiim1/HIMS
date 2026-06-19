-- Action Center: operator-set snooze so an intentionally-offline/unsupported device (or one
-- pending host-side work) can be muted from a remediation queue without faking a fix or deleting
-- the underlying finding. One snooze per (device, issue_key); expires at `until`.
CREATE TABLE remediation_snoozes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id  UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    issue_key  TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    until      TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL DEFAULT 'operator',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, issue_key)
);
CREATE INDEX idx_remediation_snooze_active ON remediation_snoozes (issue_key, until);
