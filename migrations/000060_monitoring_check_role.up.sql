-- A monitoring check is either the device's REACHABILITY check (drives the
-- device's online/offline status + the inventory counts) or a SUPPLEMENTAL
-- check (an extra port/metric the operator added — polled and shown, but it
-- must NOT flip the whole device offline or move the inventory online/offline
-- totals). Previously every check rolled up into devices.status (worst wins),
-- so adding one extra firewall-port check could mark the device offline.
ALTER TABLE monitoring_checks
    ADD COLUMN role text NOT NULL DEFAULT 'reachability'
        CHECK (role IN ('reachability', 'supplemental'));

-- Data fix for existing fleets: keep the earliest check per device as the
-- reachability check and demote any later (operator-added) checks to
-- supplemental, so a pre-existing extra check stops affecting reachability.
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY device_id ORDER BY created_at, id) AS rn
    FROM monitoring_checks
)
UPDATE monitoring_checks m
SET role = 'supplemental'
FROM ranked r
WHERE r.id = m.id AND r.rn > 1;

CREATE INDEX IF NOT EXISTS idx_monitoring_checks_device_role ON monitoring_checks (device_id, role);
