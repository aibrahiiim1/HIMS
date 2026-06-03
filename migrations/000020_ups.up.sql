-- HIMS peripherals: UPS status (UPS-MIB, RFC 1628). One current-state row per
-- UPS device; battery/charge/runtime/load updated each collection.

CREATE TABLE ups_status (
    device_id      UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    manufacturer   TEXT,
    model          TEXT,
    battery_status TEXT NOT NULL DEFAULT 'unknown'
        CHECK (battery_status IN ('normal','low','depleted','unknown')),
    charge_pct     INT,
    runtime_min    INT,
    load_pct       INT,
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
