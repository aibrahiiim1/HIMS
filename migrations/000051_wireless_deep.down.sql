DROP TABLE IF EXISTS wireless_events;
DROP TABLE IF EXISTS wireless_radio_status;
DROP TABLE IF EXISTS wireless_clients;
DROP TABLE IF EXISTS wireless_ssids;

ALTER TABLE access_points
    DROP COLUMN IF EXISTS collected_at,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS band,
    DROP COLUMN IF EXISTS firmware,
    DROP COLUMN IF EXISTS serial;

ALTER TABLE wlan_controller_info
    DROP COLUMN IF EXISTS collected_at,
    DROP COLUMN IF EXISTS ssid_count,
    DROP COLUMN IF EXISTS serial,
    DROP COLUMN IF EXISTS model,
    DROP COLUMN IF EXISTS controller_name,
    DROP COLUMN IF EXISTS profile_id,
    DROP COLUMN IF EXISTS source;
