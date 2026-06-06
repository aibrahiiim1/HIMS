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

-- name: ListQueuedAgentJobs :many
SELECT * FROM agent_jobs WHERE agent_id = $1 AND status = 'queued' ORDER BY created_at LIMIT 20;

-- name: MarkAgentJobDispatched :exec
UPDATE agent_jobs SET status = 'dispatched', dispatched_at = now() WHERE id = $1;

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
