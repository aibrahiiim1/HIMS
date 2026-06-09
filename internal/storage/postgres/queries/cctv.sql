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
INSERT INTO nvr_channels (nvr_device_id, channel_no, camera_name, camera_ip, camera_device_id, status, enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (nvr_device_id, channel_no) DO UPDATE SET
    camera_name = EXCLUDED.camera_name,
    camera_ip = EXCLUDED.camera_ip,
    camera_device_id = EXCLUDED.camera_device_id,
    status = EXCLUDED.status,
    enabled = EXCLUDED.enabled,
    last_seen_at = now()
RETURNING *;

-- name: GetNVRInfo :one
SELECT * FROM nvr_info WHERE device_id = $1;

-- name: UpsertNVRInfo :one
INSERT INTO nvr_info (device_id, manufacturer, model, serial, firmware, device_type, channel_count, hdd_count, recording, health, source)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (device_id) DO UPDATE SET
    manufacturer = EXCLUDED.manufacturer,
    model = EXCLUDED.model,
    serial = EXCLUDED.serial,
    firmware = EXCLUDED.firmware,
    device_type = EXCLUDED.device_type,
    channel_count = EXCLUDED.channel_count,
    hdd_count = EXCLUDED.hdd_count,
    recording = EXCLUDED.recording,
    health = EXCLUDED.health,
    source = EXCLUDED.source,
    collected_at = now()
RETURNING *;

-- name: ListNVRStorage :many
SELECT * FROM nvr_storage WHERE nvr_device_id = $1 ORDER BY hdd_id;

-- name: CountNVRChannels :one
-- Total camera channels collected across all recorders (CCTV summary). Channels
-- are NOT inventory devices, so this is reported separately and never folded into
-- the device count.
SELECT count(*) FROM nvr_channels;

-- name: CountLinkedNVRChannels :one
-- Channels whose camera IP matched an already-discovered standalone camera device.
SELECT count(*) FROM nvr_channels WHERE camera_device_id IS NOT NULL;

-- name: SearchNVRChannels :many
-- Global-search: NVR/DVR camera channels by channel name / camera IP / channel
-- number / recorder (NVR) name. Returns the owning recorder so a channel found
-- anywhere links back to the NVR detail page, plus any linked standalone camera
-- device. Channels are recorder-owned rows, not separate inventory devices.
SELECT ch.nvr_device_id, d.name AS nvr_name, d.category AS nvr_category,
       ch.channel_no, ch.camera_name, COALESCE(host(ch.camera_ip), '')::text AS camera_ip,
       ch.status, ch.camera_device_id
FROM nvr_channels ch
JOIN devices d ON d.id = ch.nvr_device_id AND d.deleted_at IS NULL
WHERE COALESCE(ch.camera_name,'') ILIKE '%'||$1||'%'
   OR COALESCE(host(ch.camera_ip),'') ILIKE '%'||$1||'%'
   OR CAST(ch.channel_no AS TEXT) ILIKE '%'||$1||'%'
   OR d.name ILIKE '%'||$1||'%'
ORDER BY d.name, ch.channel_no
LIMIT 50;

-- name: UpsertNVRStorage :one
INSERT INTO nvr_storage (nvr_device_id, hdd_id, name, status, capacity_mb, free_mb, property, source)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (nvr_device_id, hdd_id) DO UPDATE SET
    name = EXCLUDED.name,
    status = EXCLUDED.status,
    capacity_mb = EXCLUDED.capacity_mb,
    free_mb = EXCLUDED.free_mb,
    property = EXCLUDED.property,
    source = EXCLUDED.source,
    last_seen_at = now()
RETURNING *;
