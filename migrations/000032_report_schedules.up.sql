-- Reports Pro (#21): scheduled report delivery. A schedule generates a named
-- report on a daily/weekly/monthly cadence and emails its summary via an email
-- notification channel (which holds the SMTP target, encrypted). The full file
-- is always available on demand from the export endpoint.

CREATE TABLE report_schedules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    report_type  TEXT NOT NULL DEFAULT 'inventory'
        CHECK (report_type IN ('inventory','availability','vendors','all')),
    -- Email channel used for delivery; NULL = generate-only (no send).
    channel_id   UUID REFERENCES notification_channels(id) ON DELETE SET NULL,
    frequency    TEXT NOT NULL DEFAULT 'weekly'
        CHECK (frequency IN ('daily','weekly','monthly')),
    hour_utc     INT NOT NULL DEFAULT 6 CHECK (hour_utc BETWEEN 0 AND 23),
    enabled      BOOLEAN NOT NULL DEFAULT true,
    last_run_at  TIMESTAMPTZ,
    last_status  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
