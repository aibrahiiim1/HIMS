-- ---- Alert rules ----------------------------------------------------------

-- name: CreateAlertRule :one
INSERT INTO alert_rules (name, trigger_status, min_failures, device_category, severity, auto_work_order, work_order_priority, enabled, escalate_after_minutes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
RETURNING *;

-- name: ListAlertRules :many
SELECT * FROM alert_rules ORDER BY created_at DESC;

-- name: ListEnabledAlertRules :many
SELECT * FROM alert_rules WHERE enabled ORDER BY created_at;

-- name: SetAlertRuleEnabled :one
UPDATE alert_rules SET enabled = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = $1;

-- ---- Monitoring state for evaluation --------------------------------------

-- name: ListEnabledChecksWithDevice :many
-- The evaluator's input: every enabled check joined to its device so rules
-- can filter by category and alerts can carry a readable device name.
SELECT c.id, c.device_id, c.kind, c.target_port, c.last_status, c.consecutive_failures,
       d.name AS device_name, d.category AS device_category, d.primary_ip AS device_ip,
       d.location_id AS device_location_id
FROM monitoring_checks c
JOIN devices d ON d.id = c.device_id
WHERE c.enabled;

-- ---- Alerts ---------------------------------------------------------------

-- name: OpenAlert :one
-- Atomic open: ON CONFLICT against idx_alerts_one_open means a second open
-- for the same (rule, check) is a no-op. RETURNING yields a row ONLY on a
-- real insert, so the engine fires the work-order bridge exactly once.
INSERT INTO alerts (rule_id, device_id, check_id, severity, message)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (rule_id, check_id) WHERE status <> 'resolved' DO NOTHING
RETURNING *;

-- name: SetAlertWorkOrder :exec
UPDATE alerts SET work_order_id = $2 WHERE id = $1;

-- name: ListAlerts :many
SELECT * FROM alerts ORDER BY
    CASE status WHEN 'open' THEN 0 WHEN 'acknowledged' THEN 1 ELSE 2 END,
    opened_at DESC
LIMIT 500;

-- name: AcknowledgeAlert :one
UPDATE alerts SET status = 'acknowledged', acknowledged_at = now()
WHERE id = $1 AND status = 'open'
RETURNING *;

-- name: ResolveAlert :one
UPDATE alerts SET status = 'resolved', resolved_at = now()
WHERE id = $1 AND status <> 'resolved'
RETURNING *;

-- name: ResolveRecoveredAlerts :many
-- Auto-resolve: any un-resolved alert whose check has recovered to 'up'.
UPDATE alerts a SET status = 'resolved', resolved_at = now()
FROM monitoring_checks c
WHERE a.check_id = c.id AND a.status <> 'resolved' AND c.last_status = 'up'
RETURNING a.id, a.device_id, a.work_order_id, a.message;

-- name: GetAlert :one
SELECT * FROM alerts WHERE id = $1;

-- name: AcknowledgeAlertBy :one
UPDATE alerts SET status = 'acknowledged', acknowledged_at = now(), acknowledged_by = $2
WHERE id = $1 AND status = 'open'
RETURNING *;

-- name: EscalateStaleAlerts :many
-- Mark open, unacknowledged, not-yet-escalated alerts as escalated once they
-- have aged past their rule's escalate_after_minutes (0 = never).
UPDATE alerts a SET escalated = true, escalated_at = now()
FROM alert_rules r
WHERE a.rule_id = r.id
  AND a.status = 'open'
  AND a.escalated = false
  AND r.escalate_after_minutes > 0
  AND a.opened_at < now() - make_interval(mins => r.escalate_after_minutes)
RETURNING a.id, a.device_id, a.message, a.work_order_id, a.severity;

-- ---- Alert lifecycle timeline ---------------------------------------------

-- name: AddAlertEvent :one
INSERT INTO alert_events (alert_id, kind, actor, note)
VALUES ($1,$2,$3,$4)
RETURNING *;

-- name: ListAlertEvents :many
SELECT * FROM alert_events WHERE alert_id = $1 ORDER BY at;

-- ---- Maintenance windows (alert suppression) ------------------------------

-- name: CreateMaintenanceWindow :one
INSERT INTO maintenance_windows (scope, device_id, location_id, reason, starts_at, ends_at, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING *;

-- name: ListMaintenanceWindows :many
SELECT * FROM maintenance_windows ORDER BY starts_at DESC LIMIT 200;

-- name: ListActiveMaintenanceWindows :many
SELECT * FROM maintenance_windows WHERE now() >= starts_at AND now() < ends_at;

-- name: DeleteMaintenanceWindow :exec
DELETE FROM maintenance_windows WHERE id = $1;

-- name: OpenAlertCountsByDevice :many
-- Open (unresolved) alert counts per device, for site rollups.
SELECT device_id, COUNT(*)::bigint AS n
FROM alerts WHERE status <> 'resolved' AND device_id IS NOT NULL
GROUP BY device_id;
