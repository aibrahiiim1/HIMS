-- HIMS Operations B: Spare Parts + Purchases + Expenses.
--
-- Completes the operations layer begun in Operations A (work orders +
-- systems/licenses). Three additions:
--   * spare_parts        — stock with a reorder threshold.
--   * work_order_parts   — parts consumed by a work order (decrements stock).
--   * purchases          — capital/operating purchase records.
-- Expenses are NOT a table: they're an aggregation (a query) across
-- purchases + work-order repair cost + system license/contract cost, so the
-- numbers can never drift from their sources.

CREATE TABLE spare_parts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    sku           TEXT,
    category      TEXT NOT NULL DEFAULT 'other'
        CHECK (category IN ('cable','transceiver','disk','memory','psu','fan','board','peripheral','consumable','other')),
    -- Where the stock physically lives (a store room is a location node).
    location_id   UUID REFERENCES locations(id) ON DELETE SET NULL,
    quantity      INT NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    min_quantity  INT NOT NULL DEFAULT 0 CHECK (min_quantity >= 0),
    unit_cost     DOUBLE PRECISION NOT NULL DEFAULT 0,
    notes         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_spare_parts_category ON spare_parts (category);
-- Partial index: the low-stock view is the hot query for a parts dashboard.
CREATE INDEX idx_spare_parts_low ON spare_parts (id) WHERE quantity <= min_quantity;

-- Parts consumed by a work order. The unit cost is snapshotted at consumption
-- time so historical work-order cost is stable even if the part's price moves.
CREATE TABLE work_order_parts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    work_order_id UUID NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,
    spare_part_id UUID REFERENCES spare_parts(id) ON DELETE SET NULL,
    description   TEXT NOT NULL,
    quantity      INT NOT NULL CHECK (quantity > 0),
    unit_cost     DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_work_order_parts_wo ON work_order_parts (work_order_id);

CREATE TABLE purchases (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    description   TEXT NOT NULL,
    vendor        TEXT,
    category      TEXT NOT NULL DEFAULT 'other'
        CHECK (category IN ('hardware','software','license','contract','internet','repair','part','other')),
    location_id   UUID REFERENCES locations(id) ON DELETE SET NULL,
    system_id     UUID REFERENCES systems(id) ON DELETE SET NULL,
    device_id     UUID REFERENCES devices(id) ON DELETE SET NULL,
    amount        DOUBLE PRECISION NOT NULL DEFAULT 0,
    purchased_at  DATE NOT NULL DEFAULT CURRENT_DATE,
    invoice_ref   TEXT,
    notes         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_purchases_category ON purchases (category);
CREATE INDEX idx_purchases_location ON purchases (location_id);
CREATE INDEX idx_purchases_date ON purchases (purchased_at);
