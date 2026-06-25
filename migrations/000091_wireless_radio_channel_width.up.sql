-- Channel width per radio (e.g. "20", "40", "80", "160" MHz). Exposed by some
-- controller APIs (SmartZone, UniFi) alongside channel/power; nullable since not
-- every vendor reports it. Honest: left NULL when the API does not expose it.
ALTER TABLE wireless_radio_status
    ADD COLUMN IF NOT EXISTS channel_width TEXT NOT NULL DEFAULT '';
