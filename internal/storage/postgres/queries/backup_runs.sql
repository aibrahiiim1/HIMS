-- name: InsertBackupRun :one
INSERT INTO backup_runs (kind, status, tables, rows, size_bytes, actor, detail)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, at, kind, status, tables, rows, size_bytes, actor, detail;

-- name: InsertBackupRunWithContent :one
INSERT INTO backup_runs (kind, status, tables, rows, size_bytes, actor, detail, content)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, at, kind, status, tables, rows, size_bytes, actor, detail;

-- name: ListBackupRuns :many
-- Excludes the content blob (can be large); content is fetched on demand for download.
SELECT id, at, kind, status, tables, rows, size_bytes, actor, detail,
       (content IS NOT NULL) AS downloadable
FROM backup_runs ORDER BY at DESC LIMIT 100;

-- name: LastSuccessfulBackup :one
SELECT id, at, kind, status, tables, rows, size_bytes, actor, detail
FROM backup_runs WHERE status = 'success' ORDER BY at DESC LIMIT 1;

-- name: GetBackupRunContent :one
SELECT id, at, kind, content FROM backup_runs WHERE id = $1;

-- name: DeleteBackupRun :exec
DELETE FROM backup_runs WHERE id = $1;
