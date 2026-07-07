-- Allow the NVR-side camera-health alert condition. The "Camera offline (via NVR)"
-- rule (seeded in seedDefaultAlertRules, raised/resolved by the NVR channel monitor)
-- uses condition = 'camera_offline', which the original CHECK constraint rejected —
-- so the rule silently failed to seed and no per-camera NVR-side alerts could fire.
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_condition_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_condition_check
    CHECK (condition = ANY (ARRAY[
        'check', 'collection_stale', 'agent_offline', 'virt_collection',
        'datastore_low', 'camera_offline'
    ]));
