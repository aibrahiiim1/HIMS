-- name: CreateReportSchedule :one
INSERT INTO report_schedules (name, report_type, channel_id, frequency, hour_utc, enabled)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING *;

-- name: ListReportSchedules :many
SELECT * FROM report_schedules ORDER BY name;

-- name: GetReportSchedule :one
SELECT * FROM report_schedules WHERE id = $1;

-- name: SetReportScheduleEnabled :one
UPDATE report_schedules SET enabled = $2 WHERE id = $1 RETURNING *;

-- name: RecordReportScheduleRun :exec
UPDATE report_schedules SET last_run_at = now(), last_status = $2 WHERE id = $1;

-- name: DeleteReportSchedule :exec
DELETE FROM report_schedules WHERE id = $1;
