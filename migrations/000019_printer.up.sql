-- HIMS peripherals: printer marker supplies (Printer-MIB). One row per
-- supply (toner/ink/drum) on a printer device; the lifetime page count is a
-- device fact (printer.page_count).

CREATE TABLE printer_supplies (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id     UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    supply_index  INT NOT NULL,
    description   TEXT,
    level         BIGINT,
    max_capacity  BIGINT,
    pct           INT,            -- NULL when device reports unknown/some-remaining
    collection_source TEXT NOT NULL DEFAULT 'snmp',
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, supply_index)
);

CREATE INDEX idx_printer_supplies_device ON printer_supplies (device_id);
