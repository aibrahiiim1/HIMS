-- Wireless REST/XML primary collection: the proven vendor field mappings carry a
-- few client/AP attributes the schema lacked. Additive + nullable/defaulted so the
-- existing SNMP/SSH fallback writers are unaffected.
--   wireless_clients.snr             — signal-to-noise ratio (dB). XIQC: rss−radioNoise; ZD: rssi field.
--   wireless_clients.rx_bytes/tx_bytes — per-client traffic counters (XIQC inBytes/outBytes; ZD LEVEL=2).
--   wireless_clients.connected_since — association time, pre-formatted local string (ZD first-assoc; XIQC N/A).
--   access_points.site               — controller-reported site/zone (XIQC hostSite; ZD location/group).
--   access_points.uptime             — AP uptime string when the controller exposes it.
ALTER TABLE wireless_clients
  ADD COLUMN IF NOT EXISTS snr             INT,
  ADD COLUMN IF NOT EXISTS rx_bytes        BIGINT,
  ADD COLUMN IF NOT EXISTS tx_bytes        BIGINT,
  ADD COLUMN IF NOT EXISTS connected_since TEXT NOT NULL DEFAULT '';

ALTER TABLE access_points
  ADD COLUMN IF NOT EXISTS site   TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS uptime TEXT NOT NULL DEFAULT '';
