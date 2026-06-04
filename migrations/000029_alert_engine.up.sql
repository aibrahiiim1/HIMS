-- Alert Engine finalization (#6): maintenance windows (suppression), alert
-- lifecycle timeline, acknowledgement actor, and escalation.

-- Maintenance windows suppress alert FIRING for a scope during a time range.
-- scope='global' suppresses everything; 'device' a single device; 'site' all
-- devices whose location_id matches (exact match in v1).
CREATE TABLE maintenance_windows (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope       TEXT NOT NULL DEFAULT 'device' CHECK (scope IN ('global','site','device')),
    device_id   UUID REFERENCES devices(id) ON DELETE CASCADE,
    location_id UUID REFERENCES locations(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL DEFAULT '',
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    created_by  TEXT NOT NULL DEFAULT 'operator',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);
CREATE INDEX idx_maint_window_time ON maintenance_windows (starts_at, ends_at);
CREATE INDEX idx_maint_window_device ON maintenance_windows (device_id);

-- Alert lifecycle timeline — one row per state transition / note.
CREATE TABLE alert_events (
    id        BIGSERIAL PRIMARY KEY,
    alert_id  UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind      TEXT NOT NULL CHECK (kind IN ('opened','acknowledged','resolved','note','escalated','suppressed')),
    actor     TEXT NOT NULL DEFAULT 'system',
    note      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_alert_events_alert ON alert_events (alert_id, at);

-- Who acknowledged + escalation tracking on the alert row.
ALTER TABLE alerts ADD COLUMN acknowledged_by TEXT;
ALTER TABLE alerts ADD COLUMN escalated BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE alerts ADD COLUMN escalated_at TIMESTAMPTZ;

-- Per-rule escalation policy: minutes an open, unacknowledged alert may age
-- before it is escalated (0 = never escalate).
ALTER TABLE alert_rules ADD COLUMN escalate_after_minutes INT NOT NULL DEFAULT 0
    CHECK (escalate_after_minutes >= 0);
