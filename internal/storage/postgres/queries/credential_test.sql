-- Credential test history persistence + read models. No secrets are ever stored
-- or returned — only outcome metadata.

-- name: InsertCredentialTestRun :one
INSERT INTO credential_test_runs (actor, pairs, successes, failures, finished_at)
VALUES ($1, $2, $3, $4, now())
RETURNING id, started_at, finished_at, actor, pairs, successes, failures;

-- name: InsertCredentialTestResult :exec
INSERT INTO credential_test_results
  (run_id, device_id, credential_id, credential_name, kind, protocol, category, success, detail, latency_ms, actor, relevant, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: ListCredentialTestRuns :many
SELECT id, started_at, finished_at, actor, pairs, successes, failures
  FROM credential_test_runs
  ORDER BY started_at DESC
  LIMIT $1;

-- name: ListCredentialTestResultsByRun :many
SELECT id, run_id, device_id, credential_id, credential_name, kind, protocol,
       category, success, detail, latency_ms, tested_at, actor, source
  FROM credential_test_results
  WHERE run_id = $1
  ORDER BY success DESC, device_id;

-- name: ListDeviceCredentialTests :many
-- Full recent test history for one device (Device Detail → Credential Health).
SELECT id, run_id, device_id, credential_id, credential_name, kind, protocol,
       category, success, detail, latency_ms, tested_at, actor, relevant, source
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

-- name: LatestCCTVCredTest :one
-- The most recent ONVIF/ISAPI credential-test outcome for a device — the CCTV
-- fleet skip-guard reads this to avoid re-attempting a device that recently
-- auth-failed (which would accumulate failed logins toward a Hikvision IP
-- lockout). When two attempts share a timestamp (ONVIF + ISAPI in one batch) the
-- auth_failed row wins the tie, so a transport failure on one protocol never
-- masks an auth rejection on the other. No rows ⇒ never tested ⇒ safe to attempt.
SELECT category, success, tested_at
  FROM credential_test_results
  WHERE device_id = $1 AND protocol IN ('onvif', 'isapi')
  ORDER BY tested_at DESC, (category = 'auth_failed') DESC
  LIMIT 1;

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

-- name: DeviceCredentialSignals :many
-- Per-device aggregate over ALL credential-test outcomes (every credential, every kind),
-- so management classification NEVER loses a signal to latest-per-kind masking (e.g. a
-- legacy WSMan auth_ok_operation_fault hidden behind a sibling .\administrator auth_failed,
-- or an http_basic success hidden behind a winrm auth_failed). This is the read model for
-- the classification rule: a host is credential_failed ONLY if some credential was cleanly
-- auth-rejected AND nothing authenticated by any supported method. Booleans:
--   any_success   — any credential succeeded (deep OR web)
--   web_success   — a WEB/identity login succeeded (http_basic/http) — authenticates but is
--                   not deep management
--   legacy_authok — a credential AUTHENTICATED but the WSMan op faulted (legacy WSMan 2.0)
--                   — valid cred, needs an agent/deep collector
--   not_authorized— a credential AUTHENTICATED but the host denied access (UAC / DCOM /
--                   policy) — distinct from a wrong password
--   auth_rejected — a credential was cleanly rejected (wrong username/password)
SELECT device_id,
  bool_or(success)                                                       AS any_success,
  bool_or(success AND kind IN ('http_basic','http'))                    AS web_success,
  bool_or(category = 'auth_ok_operation_fault')                         AS legacy_authok,
  bool_or(category IN ('access_denied','wmi_access_denied'))            AS not_authorized,
  bool_or(category = 'auth_failed')                                     AS auth_rejected,
  -- wmi_broken: a credential AUTHENTICATED/reached WMI but the host's WMI repository
  -- (root\cimv2) is unavailable/corrupt — a HOST defect the agent cannot work around.
  -- Used so a legacy host whose agent collection definitively failed this way derives
  -- collection_failed (host repair) instead of looping at needs_agent.
  bool_or(category = 'namespace_unavailable')                          AS wmi_broken
FROM credential_test_results
WHERE device_id IS NOT NULL AND kind <> ''
GROUP BY device_id;
