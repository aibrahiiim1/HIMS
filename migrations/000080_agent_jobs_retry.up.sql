-- Bounded retry + per-agent dispatch throttling for relay-agent collection jobs.
-- A from-zero subnet scan enqueues one collect_os job per Windows host; without
-- retry/throttle a transient failure (agent queue saturation, temporary
-- WinRM/WMI/RPC error, timeout under concurrency pressure) became a TERMINAL
-- 'failed', and a single relay agent processed a thundering herd serially. These
-- columns let the server requeue transient failures with backoff, bound how many
-- jobs are in flight per agent, and recover jobs orphaned in 'dispatched' when an
-- agent dies mid-collection.
ALTER TABLE agent_jobs
    ADD COLUMN attempt         int         NOT NULL DEFAULT 0,
    ADD COLUMN max_attempts    int         NOT NULL DEFAULT 3,
    ADD COLUMN next_attempt_at timestamptz;

-- Find the next runnable queued jobs for an agent quickly (status + backoff gate).
CREATE INDEX IF NOT EXISTS idx_agent_jobs_runnable
    ON agent_jobs (agent_id, status, next_attempt_at);
