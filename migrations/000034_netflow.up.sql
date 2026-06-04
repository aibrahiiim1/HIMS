-- NetFlow Analytics (#12). The UDP collector decodes NetFlow v5 exports and,
-- every flush interval, writes the top talkers / protocol mix / top
-- conversations as aggregate rows. Queries roll these up over a recent window
-- for the analytics page. (Raw per-flow storage is intentionally avoided — the
-- aggregates are what operators act on, and they bound table growth.)
CREATE TABLE flow_records (
    id       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind     TEXT NOT NULL CHECK (kind IN ('talker','protocol','conversation')),
    label    TEXT NOT NULL,        -- IP / protocol name / "src→dst"
    bytes    BIGINT NOT NULL DEFAULT 0,
    packets  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX idx_flow_records_kind_at ON flow_records (kind, at DESC);
