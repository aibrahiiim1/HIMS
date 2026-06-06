DROP INDEX IF EXISTS idx_discovery_results_disposition;

ALTER TABLE discovery_results DROP CONSTRAINT IF EXISTS discovery_results_outcome_check;
ALTER TABLE discovery_results ADD CONSTRAINT discovery_results_outcome_check
    CHECK (outcome IN ('pending','alive','classified','enrolled','skipped','failed'));

ALTER TABLE discovery_results DROP COLUMN IF EXISTS disposition;
ALTER TABLE discovery_results DROP COLUMN IF EXISTS retry_count;
