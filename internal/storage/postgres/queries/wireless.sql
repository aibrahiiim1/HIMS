-- name: GetWLANControllerInfo :one
SELECT * FROM wlan_controller_info WHERE device_id = $1;

-- name: UpsertWLANControllerInfo :one
INSERT INTO wlan_controller_info (device_id, vendor, version, ap_count, client_count)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (device_id) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    version = EXCLUDED.version,
    ap_count = EXCLUDED.ap_count,
    client_count = EXCLUDED.client_count,
    last_seen_at = now()
RETURNING *;

-- name: ListAccessPoints :many
SELECT * FROM access_points WHERE controller_device_id = $1 ORDER BY name;

-- name: UpsertAccessPoint :one
INSERT INTO access_points (controller_device_id, name, mac, model, ip, status, client_count)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (controller_device_id, name) DO UPDATE SET
    mac = EXCLUDED.mac,
    model = EXCLUDED.model,
    ip = EXCLUDED.ip,
    status = EXCLUDED.status,
    client_count = EXCLUDED.client_count,
    last_seen_at = now()
RETURNING *;
