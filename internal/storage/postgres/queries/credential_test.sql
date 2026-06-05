-- Credential test history persistence + read models. No secrets are ever stored
-- or returned — only outcome metadata.

-- name: InsertCredentialTestRun :one
INSERT INTO credential_test_runs (actor, pairs, successes, failures, finished_at)
VALUES ($1, $2, $3, $4, now())
RETURNING id, started_at, finished_at, actor, pairs, successes, failures;

-- name: InsertCredentialTestResult :exec
INSERT INTO credential_test_results
  (run_id, device_id, credential_id, credential_name, kind, protocol, category, success, detail, latency_ms, actor)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListCredentialTestRuns :many
SELECT id, started_at, finished_at, actor, pairs, successes, failures
  FROM credential_test_runs
  ORDER BY started_at DESC
  LIMIT $1;

-- name: ListCredentialTestResultsByRun :many
SELECT id, run_id, device_id, credential_id, credential_name, kind, protocol,
       category, success, detail, latency_ms, tested_at, actor
  FROM credential_test_results
  WHERE run_id = $1
  ORDER BY success DESC, device_id;

-- name: ListDeviceCredentialTests :many
-- Full recent test history for one device (Device Detail → Credential Health).
SELECT id, run_id, device_id, credential_id, credential_name, kind, protocol,
       category, success, detail, latency_ms, tested_at, actor
  FROM credential_test_results
  WHERE device_id = $1
  ORDER BY tested_at DESC
  LIMIT $2;

-- name: ListCredentialCredentialTests :many
-- Recent test history for one credential (Credential Detail).
SELECT r.id, r.run_id, r.device_id, d.name AS device_name, r.credential_id,
       r.credential_name, r.kind, r.protocol, r.category, r.success, r.detail,
       r.latency_ms, r.tested_at, r.actor
  FROM credential_test_results r
  JOIN devices d ON d.id = r.device_id
  WHERE r.credential_id = $1
  ORDER BY r.tested_at DESC
  LIMIT $2;

-- name: LatestDeviceKindResults :many
-- The most recent result per (device, credential-kind). This is the read model
-- behind Management Access Coverage's test-result source, the unmanaged reasons
-- (failed / not-tested / stale), and the Inventory access filters. One row per
-- (device, kind) — the latest outcome for that protocol on that device.
SELECT DISTINCT ON (device_id, kind)
       device_id, kind, protocol, success, category, tested_at
  FROM credential_test_results
  WHERE kind <> ''
  ORDER BY device_id, kind, tested_at DESC;
