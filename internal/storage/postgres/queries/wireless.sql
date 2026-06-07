-- name: GetWLANControllerInfo :one
SELECT * FROM wlan_controller_info WHERE device_id = $1;

-- name: UpsertWLANControllerInfo :one
INSERT INTO wlan_controller_info
    (device_id, vendor, version, ap_count, client_count, source, profile_id,
     controller_name, model, serial, ssid_count, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
ON CONFLICT (device_id) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    version = EXCLUDED.version,
    ap_count = EXCLUDED.ap_count,
    client_count = EXCLUDED.client_count,
    source = EXCLUDED.source,
    profile_id = EXCLUDED.profile_id,
    controller_name = EXCLUDED.controller_name,
    model = EXCLUDED.model,
    serial = EXCLUDED.serial,
    ssid_count = EXCLUDED.ssid_count,
    collected_at = now(),
    last_seen_at = now()
RETURNING *;

-- name: ListAccessPoints :many
SELECT * FROM access_points WHERE controller_device_id = $1 ORDER BY name;

-- name: UpsertAccessPoint :one
INSERT INTO access_points
    (controller_device_id, name, mac, model, ip, status, client_count,
     serial, firmware, band, source, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
ON CONFLICT (controller_device_id, name) DO UPDATE SET
    mac = EXCLUDED.mac,
    model = EXCLUDED.model,
    ip = EXCLUDED.ip,
    status = EXCLUDED.status,
    client_count = EXCLUDED.client_count,
    serial = EXCLUDED.serial,
    firmware = EXCLUDED.firmware,
    band = EXCLUDED.band,
    source = EXCLUDED.source,
    collected_at = now(),
    last_seen_at = now()
RETURNING *;

-- name: DeleteStaleAccessPoints :exec
-- Prune APs for a controller not refreshed in the latest collection of a source.
DELETE FROM access_points
WHERE controller_device_id = $1 AND source = $2 AND collected_at < $3;

-- name: UpsertWirelessSSID :one
INSERT INTO wireless_ssids
    (controller_device_id, name, status, security, band, vlan, client_count, source, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
ON CONFLICT (controller_device_id, name) DO UPDATE SET
    status = EXCLUDED.status,
    security = EXCLUDED.security,
    band = EXCLUDED.band,
    vlan = EXCLUDED.vlan,
    client_count = EXCLUDED.client_count,
    source = EXCLUDED.source,
    collected_at = now()
RETURNING *;

-- name: ListWirelessSSIDs :many
SELECT * FROM wireless_ssids WHERE controller_device_id = $1 ORDER BY name;

-- name: DeleteStaleWirelessSSIDs :exec
DELETE FROM wireless_ssids
WHERE controller_device_id = $1 AND source = $2 AND collected_at < $3;

-- name: UpsertWirelessClient :one
INSERT INTO wireless_clients
    (controller_device_id, mac, ip, hostname, ap_name, ssid, rssi, band, source, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
ON CONFLICT (controller_device_id, mac) DO UPDATE SET
    ip = EXCLUDED.ip,
    hostname = EXCLUDED.hostname,
    ap_name = EXCLUDED.ap_name,
    ssid = EXCLUDED.ssid,
    rssi = EXCLUDED.rssi,
    band = EXCLUDED.band,
    source = EXCLUDED.source,
    collected_at = now()
RETURNING *;

-- name: ListWirelessClients :many
SELECT * FROM wireless_clients WHERE controller_device_id = $1 ORDER BY ap_name, mac;

-- name: DeleteStaleWirelessClients :exec
DELETE FROM wireless_clients
WHERE controller_device_id = $1 AND source = $2 AND collected_at < $3;

-- name: UpsertWirelessRadio :one
INSERT INTO wireless_radio_status
    (controller_device_id, ap_name, radio, band, channel, power_dbm, client_count, source, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
ON CONFLICT (controller_device_id, ap_name, radio) DO UPDATE SET
    band = EXCLUDED.band,
    channel = EXCLUDED.channel,
    power_dbm = EXCLUDED.power_dbm,
    client_count = EXCLUDED.client_count,
    source = EXCLUDED.source,
    collected_at = now()
RETURNING *;

-- name: ListWirelessRadios :many
SELECT * FROM wireless_radio_status WHERE controller_device_id = $1 ORDER BY ap_name, radio;

-- name: DeleteStaleWirelessRadios :exec
DELETE FROM wireless_radio_status
WHERE controller_device_id = $1 AND source = $2 AND collected_at < $3;

-- name: InsertWirelessEvent :exec
INSERT INTO wireless_events (controller_device_id, at, severity, category, message, source)
VALUES ($1,$2,$3,$4,$5,$6);

-- name: ListWirelessEvents :many
SELECT * FROM wireless_events WHERE controller_device_id = $1 ORDER BY at DESC LIMIT $2;

-- name: DeleteWirelessEventsForSource :exec
-- Replace an event set for a source (collectors re-publish the current window).
DELETE FROM wireless_events WHERE controller_device_id = $1 AND source = $2;
