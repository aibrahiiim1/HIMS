-- HIMS: make device duplication impossible at the database level.
--
-- The discovery reconciler previously keyed on (primary_ip, location) with no
-- DB constraint, so the same physical device scanned under different scopes
-- (with a site vs without) produced separate rows, and concurrent jobs could
-- race to insert. We enforce ONE live device per IP — the reconciler matches by
-- primary_ip alone, and this index is the hard backstop.
--
-- Note: this intentionally drops "same IP in two different hotels" as distinct
-- devices (the fleet is one flat routed space, so IPs are globally unique). If
-- true multi-hotel overlapping IPs are ever needed, replace with a unique index
-- on (primary_ip, location_id) NULLS NOT DISTINCT and a scope-aware reconciler.

-- 1. Dedupe existing live devices: keep the most-recently-updated row per IP,
--    hard-delete the rest (their inventory child rows cascade away).
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY primary_ip ORDER BY updated_at DESC) AS rn
    FROM devices
    WHERE deleted_at IS NULL AND primary_ip IS NOT NULL
)
DELETE FROM devices WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

-- 2. Enforce uniqueness for live, IP-bearing devices (manual no-IP assets and
--    soft-deleted rows are exempt).
CREATE UNIQUE INDEX idx_devices_unique_primary_ip
    ON devices (primary_ip)
    WHERE deleted_at IS NULL AND primary_ip IS NOT NULL;
