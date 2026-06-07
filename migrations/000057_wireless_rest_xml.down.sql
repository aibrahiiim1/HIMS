ALTER TABLE wireless_clients
  DROP COLUMN IF EXISTS snr,
  DROP COLUMN IF EXISTS rx_bytes,
  DROP COLUMN IF EXISTS tx_bytes,
  DROP COLUMN IF EXISTS connected_since;

ALTER TABLE access_points
  DROP COLUMN IF EXISTS site,
  DROP COLUMN IF EXISTS uptime;
