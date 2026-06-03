-- name: DeviceCountByCategory :many
SELECT category, COUNT(*) AS count
FROM devices WHERE deleted_at IS NULL
GROUP BY category ORDER BY count DESC, category;

-- name: DeviceCountByStatus :many
SELECT status, COUNT(*) AS count
FROM devices WHERE deleted_at IS NULL
GROUP BY status ORDER BY count DESC;

-- name: CountOpenWorkOrders :one
SELECT COUNT(*) FROM work_orders WHERE status NOT IN ('solved','closed');

-- name: CountOpenAlerts :one
SELECT COUNT(*) FROM alerts WHERE status = 'open';

-- name: CountExpiringSystems :one
-- Systems whose license OR support expires within 90 days (or already has).
SELECT COUNT(*) FROM systems
WHERE (license_expiry IS NOT NULL AND license_expiry <= CURRENT_DATE + 90)
   OR (support_expiry IS NOT NULL AND support_expiry <= CURRENT_DATE + 90);

-- name: TotalExpenses :one
SELECT COALESCE(SUM(amount),0)::double precision FROM purchases;

-- name: CountDevicesNeedingAttention :one
SELECT COUNT(*) FROM devices WHERE deleted_at IS NULL AND status IN ('down','warning');
