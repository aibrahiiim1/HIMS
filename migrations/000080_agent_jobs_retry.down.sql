DROP INDEX IF EXISTS idx_agent_jobs_runnable;
ALTER TABLE agent_jobs
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS max_attempts,
    DROP COLUMN IF EXISTS attempt;
