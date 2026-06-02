-- HIMS Phase 5 (Operations A): Work Orders + Systems & Licenses.
--
-- Work orders are the mini-ITSM core: asset-linked tickets with a lifecycle
-- and an append-only event timeline. Systems & Licenses is the register of
-- software systems / contracts with license + support expiry tracking
-- (status — active/expiring/expired — is computed at read time, not stored,
-- so it's always current relative to "today").

CREATE TABLE work_orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Optional asset link; a work order may be general (no device).
    device_id      UUID REFERENCES devices(id) ON DELETE SET NULL,
    location_id    UUID REFERENCES locations(id) ON DELETE SET NULL,
    title          TEXT NOT NULL,
    problem_type   TEXT NOT NULL DEFAULT 'other'
        CHECK (problem_type IN ('hardware','software','network','license','other')),
    priority       TEXT NOT NULL DEFAULT 'medium'
        CHECK (priority IN ('low','medium','high','critical')),
    status         TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','in_progress','waiting','solved','closed')),
    assigned_to    TEXT,
    diagnosis      TEXT,
    action_taken   TEXT,
    spare_parts    TEXT,          -- free text in Operations A; stock link is Operations B
    external_vendor TEXT,
    cost           DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at    TIMESTAMPTZ
);

CREATE INDEX idx_work_orders_device ON work_orders (device_id);
CREATE INDEX idx_work_orders_status ON work_orders (status);
CREATE INDEX idx_work_orders_priority ON work_orders (priority);

-- Append-only timeline: every status change / note becomes an event.
CREATE TABLE work_order_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    work_order_id UUID NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,
    event_type    TEXT NOT NULL DEFAULT 'note'
        CHECK (event_type IN ('created','status_change','note','assignment','cost')),
    note          TEXT,
    actor         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_work_order_events_wo ON work_order_events (work_order_id, created_at);

-- Systems & Licenses register: software systems / contracts and their expiry.
CREATE TABLE systems (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    vendor          TEXT,
    -- Scope: NULL location = group-wide; otherwise pinned to a hotel/site.
    location_id     UUID REFERENCES locations(id) ON DELETE SET NULL,
    license_expiry  DATE,
    support_expiry  DATE,
    cost            DOUBLE PRECISION NOT NULL DEFAULT 0,
    notes           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_systems_license_expiry ON systems (license_expiry);
