-- Asset Lifecycle (#18): warranty / EOL / owner / cost per device. Kept in a
-- 1:1 side table so the core devices row stays lean and lifecycle is optional.
-- Maintenance history is the device's linked work orders (#19) — not duplicated
-- here. Lifecycle statuses (in-warranty / approaching-EOL / …) are derived at
-- read time from the dates, never stored.
CREATE TABLE device_lifecycle (
    device_id       UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    owner           TEXT NOT NULL DEFAULT '',
    supplier        TEXT NOT NULL DEFAULT '',
    purchase_date   DATE,
    warranty_expiry DATE,
    eol_date        DATE,
    cost            DOUBLE PRECISION NOT NULL DEFAULT 0,
    notes           TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
