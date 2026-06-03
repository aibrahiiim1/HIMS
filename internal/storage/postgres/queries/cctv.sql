-- name: GetCameraInfo :one
SELECT * FROM camera_info WHERE device_id = $1;

-- name: UpsertCameraInfo :one
INSERT INTO camera_info (device_id, manufacturer, model, resolution, rtsp_url, onvif_url)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (device_id) DO UPDATE SET
    manufacturer = EXCLUDED.manufacturer,
    model = EXCLUDED.model,
    resolution = EXCLUDED.resolution,
    rtsp_url = EXCLUDED.rtsp_url,
    onvif_url = EXCLUDED.onvif_url,
    last_seen_at = now()
RETURNING *;

-- name: ListNVRChannels :many
SELECT * FROM nvr_channels WHERE nvr_device_id = $1 ORDER BY channel_no;

-- name: UpsertNVRChannel :one
INSERT INTO nvr_channels (nvr_device_id, channel_no, camera_name, camera_ip, camera_device_id, status)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (nvr_device_id, channel_no) DO UPDATE SET
    camera_name = EXCLUDED.camera_name,
    camera_ip = EXCLUDED.camera_ip,
    camera_device_id = EXCLUDED.camera_device_id,
    status = EXCLUDED.status,
    last_seen_at = now()
RETURNING *;
