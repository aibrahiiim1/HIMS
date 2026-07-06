-- ---- Alert rules ----------------------------------------------------------

-- name: CreateAlertRule :one
INSERT INTO alert_rules (name, trigger_status, min_failures, device_category, severity, auto_work_order, work_order_priority, enabled, escalate_after_minutes, condition, warn_threshold, crit_threshold)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING *;

-- name: ListAlertRules :many
SELECT * FROM alert_rules ORDER BY created_at DESC;

-- name: ListEnabledAlertRules :many
-- Check-status rules only — the check engine matches these against monitoring checks.
SELECT * FROM alert_rules WHERE enabled AND condition = 'check' ORDER BY created_at;

-- name: ListEnabledStateRules :many
-- State-based rules — evaluated by the device-state evaluator (api/alert_state.go), not checks.
SELECT * FROM alert_rules WHERE enabled AND condition <> 'check' ORDER BY created_at;

-- name: SetAlertRuleEnabled :one
UPDATE alert_rules SET enabled = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = $1;

-- ---- Monitoring state for evaluation --------------------------------------

-- name: ListEnabledChecksWithDevice :many
-- The evaluator's input: every enabled check joined to its device so rules
-- can filter by category and alerts can carry a readable device name.
-- SUPPLEMENTAL checks (e.g. SNMP sysUpTime health) are EXCLUDED — they only
-- degrade a device to "warning" for visibility and must NEVER raise an alert.
SELECT c.id, c.device_id, c.kind, c.target_port, c.last_status, c.consecutive_failures,
       d.name AS device_name, d.category AS device_category, d.primary_ip AS device_ip,
       d.location_id AS device_location_id
FROM monitoring_checks c
JOIN devices d ON d.id = c.device_id
WHERE c.enabled AND c.role IS DISTINCT FROM 'supplemental';

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

-- name: ResolveAlertsForDeviceTCPChecks :exec
-- Resolve open alerts bound to a device's TCP reachability checks BEFORE those
-- checks are deleted/replaced. Without this, the alerts.check_id ON DELETE SET NULL
-- would orphan the alert (check_id -> NULL, empty fingerprint), where it collides
-- with idx_alerts_state_one_open on the next such deletion. Resolving first keeps
-- the delete safe and closes an alert whose underlying check no longer exists.
UPDATE alerts a SET status = 'resolved', resolved_at = now()
FROM monitoring_checks c
WHERE a.check_id = c.id AND a.status <> 'resolved'
  AND c.device_id = $1 AND c.kind = 'tcp';

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

-- ---- State-based alerts (no check_id; dedup on rule_id + fingerprint) ------

-- name: OpenStateAlert :one
-- Opens a state alert. The evaluator checks existence first (single-threaded sweep), and the
-- partial unique index (rule_id, fingerprint) WHERE check_id IS NULL is the race backstop.
INSERT INTO alerts (rule_id, device_id, check_id, severity, status, message, fingerprint)
VALUES ($1, $2, NULL, $3, 'open', $4, $5)
RETURNING *;

-- name: ListOpenStateAlertsByRule :many
SELECT id, device_id, fingerprint FROM alerts
WHERE rule_id = $1 AND check_id IS NULL AND status <> 'resolved';

-- name: OpenStateAlertIfAbsent :exec
-- Idempotent open for a state alert (e.g. NVR-side camera offline): if an open
-- alert with this (rule, fingerprint) already exists, do nothing — never errors on
-- a duplicate (unlike OpenStateAlert, which the .204 collision showed can 500).
-- The transition-driven caller (NVR channel monitor) uses this so re-running the
-- poll while a camera stays offline can't raise a second alert.
INSERT INTO alerts (rule_id, device_id, check_id, severity, status, message, fingerprint)
VALUES ($1, $2, NULL, $3, 'open', $4, $5)
ON CONFLICT (rule_id, fingerprint) WHERE check_id IS NULL AND status <> 'resolved' DO NOTHING;

-- name: ResolveStateAlertByFingerprint :exec
-- Resolve any open state alert for (rule, fingerprint) — e.g. a camera that came
-- back online on its NVR. Idempotent: a no-op when nothing is open.
UPDATE alerts SET status = 'resolved', resolved_at = now()
WHERE rule_id = $1 AND fingerprint = $2 AND check_id IS NULL AND status <> 'resolved';

-- name: ResolveAlertByID :one
UPDATE alerts SET status = 'resolved', resolved_at = now() WHERE id = $1 AND status <> 'resolved' RETURNING *;

-- name: DeviceCollectionRecency :many
-- Most recent successful collection signal per device (deep OS inventory, ok virtualization
-- collection, or a successful credential test) — feeds the collection-stale state alert.
SELECT d.id AS device_id, d.name, d.primary_ip,
  GREATEST(
    COALESCE((SELECT max(collected_at) FROM os_inventory oi WHERE oi.device_id = d.id), 'epoch'),
    COALESCE((SELECT max(collected_at) FROM vh_collection_health h WHERE h.device_id = d.id AND h.status = 'ok'), 'epoch'),
    COALESCE((SELECT max(tested_at) FROM credential_test_results c WHERE c.device_id = d.id AND c.success), 'epoch')
  )::timestamptz AS last_success
FROM devices d WHERE d.deleted_at IS NULL;

-- name: ListAllCollectionHealth :many
SELECT h.device_id, d.name, d.primary_ip, h.collector, h.status, h.detail, h.vm_count, h.collected_at
FROM vh_collection_health h JOIN devices d ON d.id = h.device_id;

-- name: ListAllDatastores :many
SELECT ds.host_device_id, d.name AS host_name, d.primary_ip, ds.name, ds.capacity_bytes, ds.free_bytes
FROM vh_datastores ds JOIN devices d ON d.id = ds.host_device_id WHERE ds.capacity_bytes > 0;

-- name: ListAlertsEnriched :many
-- Alerts joined with their rule (name + condition) and device (name/ip/category/site) so the
-- Alerts page can group, filter, and show clear per-alert context without extra round-trips.
SELECT a.id, a.rule_id, a.device_id, a.check_id, a.severity, a.status, a.message,
       a.fingerprint, a.work_order_id, a.opened_at, a.acknowledged_at, a.acknowledged_by,
       a.escalated, a.escalated_at, a.resolved_at,
       r.name AS rule_name, r.condition AS condition,
       r.warn_threshold, r.crit_threshold,
       d.name AS device_name, d.primary_ip AS device_ip, d.category AS device_category,
       d.location_id AS device_location
FROM alerts a
JOIN alert_rules r ON r.id = a.rule_id
LEFT JOIN devices d ON d.id = a.device_id
ORDER BY CASE a.status WHEN 'open' THEN 0 WHEN 'acknowledged' THEN 1 ELSE 2 END, a.opened_at DESC
LIMIT 2000;
