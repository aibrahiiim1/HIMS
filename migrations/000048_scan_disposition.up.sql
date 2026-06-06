-- Known Device Retry / Scan Stability.
--
-- A scan result now carries a `disposition` so a KNOWN device (already in
-- inventory for a scope IP) never silently vanishes from a job, and so the
-- per-job counts can be split honestly:
--   newly_discovered   — alive, first time we have seen this IP as a device
--   known_seen         — alive, device was already in inventory
--   known_recovered    — known device missed in the main sweep, found by targeted retry
--   known_missed       — known device missed, not retried (no last-known data)
--   known_unreachable  — known device missed in the sweep AND after targeted retries
-- retry_count records how many targeted retries were spent on a missed known device.
ALTER TABLE discovery_results
    ADD COLUMN disposition TEXT NOT NULL DEFAULT '',
    ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;

-- A missed known device gets a row even though it never went "alive": add a
-- dedicated 'missed' outcome so it is distinct from a deliberately-skipped host.
ALTER TABLE discovery_results DROP CONSTRAINT IF EXISTS discovery_results_outcome_check;
ALTER TABLE discovery_results ADD CONSTRAINT discovery_results_outcome_check
    CHECK (outcome IN ('pending','alive','classified','enrolled','skipped','failed','missed'));

CREATE INDEX idx_discovery_results_disposition ON discovery_results (disposition);
