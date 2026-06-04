-- ---- Notification channels ------------------------------------------------

-- name: CreateNotificationChannel :one
INSERT INTO notification_channels (name, type, target_encrypted, key_id, min_severity, quiet_start_min, quiet_end_min, enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels ORDER BY created_at;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = $1;

-- name: SetNotificationChannelEnabled :one
UPDATE notification_channels SET enabled = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: DeleteNotificationChannel :exec
DELETE FROM notification_channels WHERE id = $1;

-- ---- Delivery log ---------------------------------------------------------

-- name: InsertNotificationLog :one
-- The unique index idx_notif_once makes a duplicate 'sent' for the same
-- (channel, alert) a no-op, so RETURNING yields a row only on a real insert.
INSERT INTO notification_log (channel_id, alert_id, status, detail)
VALUES ($1,$2,$3,$4)
ON CONFLICT (channel_id, alert_id) WHERE status = 'sent' AND alert_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: ListNotificationLog :many
SELECT * FROM notification_log ORDER BY at DESC LIMIT 200;

-- name: ListSentNotificationPairs :many
-- (channel_id, alert_id) pairs already delivered, so the dispatcher skips them.
SELECT channel_id, alert_id FROM notification_log WHERE status = 'sent' AND alert_id IS NOT NULL;

-- name: ListNotifiableAlerts :many
-- Alerts worth notifying about: still open or escalated, opened recently.
SELECT a.id, a.severity, a.message, a.escalated, a.opened_at, a.device_id
FROM alerts a
WHERE a.status <> 'resolved' AND a.opened_at > now() - interval '24 hours'
ORDER BY a.opened_at DESC
LIMIT 500;
