-- Notifications (#7): outbound alert delivery channels + delivery log.
-- The channel "target" (webhook URL, bot token, SMTP password/recipients) is a
-- secret, so it is stored AES-256-GCM encrypted (same cipher as credentials)
-- and never returned by the API.

CREATE TABLE notification_channels (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL,
    type             TEXT NOT NULL CHECK (type IN ('slack','teams','telegram','webhook','email')),
    target_encrypted BYTEA NOT NULL,            -- AES-GCM blob of the target JSON (secret)
    key_id           TEXT NOT NULL,
    min_severity     TEXT NOT NULL DEFAULT 'warning' CHECK (min_severity IN ('info','warning','critical')),
    quiet_start_min  INT,                        -- minutes-of-day [0,1440); NULL = no quiet hours
    quiet_end_min    INT,
    enabled          BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE notification_log (
    id         BIGSERIAL PRIMARY KEY,
    channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    alert_id   UUID REFERENCES alerts(id) ON DELETE SET NULL,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    status     TEXT NOT NULL CHECK (status IN ('sent','failed','skipped','test')),
    detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_notif_log_channel ON notification_log (channel_id, at DESC);

-- At most one successful delivery per (channel, alert): the dispatcher inserts
-- 'sent' with ON CONFLICT DO NOTHING so a still-open alert is not re-notified.
CREATE UNIQUE INDEX idx_notif_once ON notification_log (channel_id, alert_id)
    WHERE status = 'sent' AND alert_id IS NOT NULL;
