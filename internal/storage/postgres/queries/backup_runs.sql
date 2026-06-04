-- name: InsertBackupRun :one
INSERT INTO backup_runs (kind, status, tables, rows, size_bytes, actor, detail)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: ListBackupRuns :many
SELECT * FROM backup_runs ORDER BY at DESC LIMIT 100;

-- name: LastSuccessfulBackup :one
SELECT * FROM backup_runs WHERE status = 'success' ORDER BY at DESC LIMIT 1;
