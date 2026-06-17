-- Relay Agent / Site Collector persistence. No secrets stored here; the agent
-- token is stored only as a SHA-256 hash.

-- name: CreateRelayAgent :one
INSERT INTO relay_agents (name, location_id, token_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListRelayAgents :many
SELECT * FROM relay_agents ORDER BY name;

-- name: GetRelayAgent :one
SELECT * FROM relay_agents WHERE id = $1;

-- name: GetRelayAgentByToken :one
SELECT * FROM relay_agents WHERE token_hash = $1;

-- name: UpdateRelayAgentIdentity :exec
UPDATE relay_agents
SET hostname = $2, ip = $3, os = $4, version = $5, capabilities = $6,
    status = 'online', last_heartbeat = now(), updated_at = now()
WHERE id = $1;

-- name: RelayAgentHeartbeat :exec
UPDATE relay_agents
SET status = CASE WHEN enabled THEN 'online' ELSE 'disabled' END,
    version = COALESCE(NULLIF($2, ''), version),
    last_heartbeat = now(), last_error = COALESCE(NULLIF($3, ''), last_error), updated_at = now()
WHERE id = $1;

-- name: SetRelayAgentEnabled :exec
UPDATE relay_agents
SET enabled = $2, status = CASE WHEN $2 THEN status ELSE 'disabled' END, updated_at = now()
WHERE id = $1;

-- name: SetRelayAgentLocation :exec
UPDATE relay_agents SET location_id = $2, updated_at = now() WHERE id = $1;

-- name: SetRelayAgentToken :exec
-- Rotate an agent's enrollment token (only the new hash is stored). The previous
-- token stops working immediately; the operator re-downloads a fresh installer.
UPDATE relay_agents SET token_hash = $2, updated_at = now() WHERE id = $1;

-- name: DeleteRelayAgent :exec
DELETE FROM relay_agents WHERE id = $1;

-- name: ResolveSiteAgent :one
-- The newest enabled, recently-online agent assigned to a location — used to
-- prefer agent collection for devices in that site.
SELECT * FROM relay_agents
WHERE location_id = $1 AND enabled AND status = 'online'
ORDER BY last_heartbeat DESC NULLS LAST
LIMIT 1;

-- name: CreateAgentJob :one
INSERT INTO agent_jobs (agent_id, device_id, credential_id, kind, protocol, target, request)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListRunnableAgentJobs :many
-- The next queued jobs for an agent that are ready to run now (backoff elapsed),
-- capped by $2 = the per-agent dispatch budget (cap - in-flight dispatched). This
-- is what bounds the thundering herd: the server never hands one agent more than
-- the cap of concurrent collect jobs.
SELECT * FROM agent_jobs
WHERE agent_id = $1 AND status = 'queued'
  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
ORDER BY created_at
LIMIT $2;

-- name: CountDispatchedAgentJobs :one
-- Jobs currently handed to the agent and not yet reported back — the in-flight
-- count subtracted from the dispatch cap to compute the poll budget.
SELECT count(*) FROM agent_jobs WHERE agent_id = $1 AND status = 'dispatched';

-- name: MarkAgentJobDispatched :exec
UPDATE agent_jobs SET status = 'dispatched', dispatched_at = now() WHERE id = $1;

-- name: RequeueAgentJob :exec
-- Return a transiently-failed job to the queue with an incremented attempt and a
-- backoff deadline ($2). Clears dispatched_at so it can be re-dispatched once the
-- backoff elapses. Used for retryable (non-auth) collection failures.
UPDATE agent_jobs
SET status = 'queued', attempt = attempt + 1, next_attempt_at = $2,
    dispatched_at = NULL, error = $3, category = $4
WHERE id = $1;

-- name: RequeueStaleAgentJobs :execrows
-- Recover jobs stuck 'dispatched' whose agent never reported back (agent crash /
-- dropped connection): requeue (bumped attempt) if attempts remain, else mark
-- failed so they never block re-enqueue forever. $1 = dispatched-before cutoff.
UPDATE agent_jobs
SET status          = CASE WHEN attempt + 1 >= max_attempts THEN 'failed' ELSE 'queued' END,
    attempt         = attempt + 1,
    dispatched_at   = NULL,
    next_attempt_at = CASE WHEN attempt + 1 >= max_attempts THEN NULL ELSE now() END,
    finished_at     = CASE WHEN attempt + 1 >= max_attempts THEN now() ELSE finished_at END,
    error           = CASE WHEN attempt + 1 >= max_attempts
                           THEN 'agent did not report a result (stale dispatched; gave up after max attempts)'
                           ELSE error END,
    category        = CASE WHEN attempt + 1 >= max_attempts THEN 'agent_no_result' ELSE category END
WHERE kind = 'collect_os' AND status = 'dispatched'
  AND dispatched_at IS NOT NULL AND dispatched_at < $1;

-- name: ListDevicesWithActiveAgentJobs :many
-- Device ids with an in-flight collect_os job (queued or dispatched). Feeds the
-- pending_collection management state so an in-flight host is not misreported as a
-- terminal failure from its stale direct-probe attempt.
SELECT DISTINCT device_id FROM agent_jobs
WHERE kind = 'collect_os' AND status IN ('queued', 'dispatched') AND device_id IS NOT NULL;

-- name: AgentJobStatusCounts :many
-- Fleet-wide collect-job rollup by status (acceptance / queue-visibility report).
SELECT status::text AS status, count(*) AS n FROM agent_jobs GROUP BY status;

-- name: CountAgentJobsByStatusForAgent :many
-- Per-agent job rollup by status (queued / dispatched / done / failed).
SELECT status::text AS status, count(*) AS n FROM agent_jobs WHERE agent_id = $1 GROUP BY status;

-- name: CollectionProgressForJob :one
-- Collect-job rollup for the devices a discovery job enrolled — lets the UI/API show
-- "Discovery complete · collecting N" while deep collection drains AFTER the scan's
-- probe/enroll phase finishes. retry_waiting (backoff) is split from queued so the
-- operator sees jobs that are waiting vs ready. The scan is "settled" when
-- queued + retry_waiting + running all reach 0.
SELECT
  count(*) FILTER (WHERE status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= now()))::bigint AS queued,
  count(*) FILTER (WHERE status = 'queued' AND next_attempt_at > now())::bigint               AS retry_waiting,
  count(*) FILTER (WHERE status = 'dispatched')::bigint                                        AS running,
  count(*) FILTER (WHERE status = 'done')::bigint                                              AS done,
  count(*) FILTER (WHERE status = 'failed')::bigint                                            AS failed
FROM agent_jobs
WHERE kind = 'collect_os' AND device_id IN (
  SELECT device_id FROM discovery_results WHERE job_id = $1 AND device_id IS NOT NULL
);

-- name: GetAgentJob :one
SELECT * FROM agent_jobs WHERE id = $1;

-- name: CompleteAgentJob :exec
UPDATE agent_jobs
SET status = $2, result = $3, category = $4, error = $5, finished_at = now()
WHERE id = $1;

-- name: ListAgentJobs :many
SELECT id, agent_id, device_id, kind, protocol, target, status, category, error, created_at, dispatched_at, finished_at
FROM agent_jobs WHERE agent_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: CountActiveDeviceAgentJobs :one
-- In-flight collection jobs for a device (queued or dispatched) — used to avoid
-- enqueuing a duplicate when a scan re-routes the same device to its site agent.
SELECT count(*) FROM agent_jobs
WHERE device_id = $1 AND kind = 'collect_os' AND status IN ('queued', 'dispatched');

-- name: ListRecentAgentJobsAll :many
-- Recent jobs across all agents (fleet-wide failed-job / Data Quality views).
SELECT id, agent_id, device_id, kind, protocol, target, status, category, error, created_at, dispatched_at, finished_at
FROM agent_jobs ORDER BY created_at DESC LIMIT $1;

-- name: CountFailedAgentJobs :one
-- Failed jobs for one agent (for the agent detail page + Data Quality count).
SELECT count(*) FROM agent_jobs WHERE agent_id = $1 AND status = 'failed';
