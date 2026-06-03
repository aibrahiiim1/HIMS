-- HIMS Monitoring 6B: Alerting engine + alert→work-order bridge.
--
-- Rule-based alerting over the state the monitoring engine already produces.
-- A rule matches checks by their current status (+ optional consecutive-
-- failure floor and device-category filter); when a matching check has no
-- open alert, one is opened, and — if the rule says so — a work order is
-- auto-created and linked. Alerts auto-resolve when their check recovers.
--
-- This closes the alert→work-order bridge that Operations B and the
-- Monitoring engine both pointed at. SNMP-metric checks (the other half of
-- the original 6B) need credential-decrypt infrastructure that doesn't exist
-- yet and are split out to 6C.

CREATE TABLE alert_rules (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    -- Match condition: a check whose last_status equals trigger_status and
    -- whose consecutive_failures >= min_failures fires this rule.
    trigger_status TEXT NOT NULL DEFAULT 'down'
        CHECK (trigger_status IN ('down','warning')),
    min_failures  INT NOT NULL DEFAULT 1 CHECK (min_failures >= 0),
    -- NULL category = applies to every device; otherwise only that category.
    device_category TEXT,
    severity      TEXT NOT NULL DEFAULT 'warning'
        CHECK (severity IN ('info','warning','critical')),
    -- Bridge: when true, firing this rule auto-creates a linked work order.
    auto_work_order BOOLEAN NOT NULL DEFAULT false,
    work_order_priority TEXT NOT NULL DEFAULT 'high'
        CHECK (work_order_priority IN ('low','medium','high','critical')),
    enabled       BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE alerts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id       UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    device_id     UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    check_id      UUID REFERENCES monitoring_checks(id) ON DELETE SET NULL,
    severity      TEXT NOT NULL DEFAULT 'warning'
        CHECK (severity IN ('info','warning','critical')),
    status        TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','acknowledged','resolved')),
    message       TEXT NOT NULL,
    -- The bridge link: the work order this alert spawned (if any).
    work_order_id UUID REFERENCES work_orders(id) ON DELETE SET NULL,
    opened_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    resolved_at   TIMESTAMPTZ
);

CREATE INDEX idx_alerts_status ON alerts (status);
CREATE INDEX idx_alerts_device ON alerts (device_id);

-- At most one un-resolved alert per (rule, check): the engine opens alerts
-- with ON CONFLICT DO NOTHING against this index, so a flapping check can't
-- pile up duplicate open alerts (atomic open).
CREATE UNIQUE INDEX idx_alerts_one_open ON alerts (rule_id, check_id)
    WHERE status <> 'resolved';
