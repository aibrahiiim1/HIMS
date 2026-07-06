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

-- name: UpsertCameraEnrichment :exec
-- Enriched read-only camera facts from ISAPI (NIC + time + firmware/serial).
-- COALESCE keeps an existing value when a re-collect doesn't re-resolve a field.
INSERT INTO camera_info (device_id, device_name, firmware, serial, mac_address, ip_address, subnet_mask, gateway, dns_server, ntp_server, time_zone)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (device_id) DO UPDATE SET
    device_name = COALESCE(NULLIF(EXCLUDED.device_name,''), camera_info.device_name),
    firmware    = COALESCE(NULLIF(EXCLUDED.firmware,''), camera_info.firmware),
    serial      = COALESCE(NULLIF(EXCLUDED.serial,''), camera_info.serial),
    mac_address = COALESCE(NULLIF(EXCLUDED.mac_address,''), camera_info.mac_address),
    ip_address  = COALESCE(NULLIF(EXCLUDED.ip_address,''), camera_info.ip_address),
    subnet_mask = COALESCE(NULLIF(EXCLUDED.subnet_mask,''), camera_info.subnet_mask),
    gateway     = COALESCE(NULLIF(EXCLUDED.gateway,''), camera_info.gateway),
    dns_server  = COALESCE(NULLIF(EXCLUDED.dns_server,''), camera_info.dns_server),
    ntp_server  = COALESCE(NULLIF(EXCLUDED.ntp_server,''), camera_info.ntp_server),
    time_zone   = COALESCE(NULLIF(EXCLUDED.time_zone,''), camera_info.time_zone),
    last_seen_at = now();

-- name: ListNVRChannels :many
SELECT * FROM nvr_channels WHERE nvr_device_id = $1 ORDER BY channel_no;

-- name: ListRecorderDevices :many
-- Managed NVR/DVR recorders (camera aggregators) with an IP — the fleet the
-- NVR-side channel-health monitor re-polls on a cadence. Excludes deleted devices.
SELECT * FROM devices
WHERE deleted_at IS NULL AND primary_ip IS NOT NULL AND category IN ('nvr','dvr')
ORDER BY primary_ip;

-- name: ListOfflineNVRChannels :many
-- Cameras an NVR/DVR reports OFFLINE, with the recorder + the reason (network
-- unreachable / credential error / …), so Data Quality surfaces every disconnected
-- camera and WHY — not just an online/offline flag.
SELECT ch.nvr_device_id, nvr.name AS nvr_name, ch.channel_no,
       COALESCE(host(ch.camera_ip),'')::text AS camera_ip,
       COALESCE(ch.camera_name,'')::text AS camera_name,
       ch.detect_reason
FROM nvr_channels ch
JOIN devices nvr ON nvr.id = ch.nvr_device_id AND nvr.deleted_at IS NULL
WHERE ch.status = 'offline'
ORDER BY nvr.name, ch.channel_no;

-- name: ListNVRChannelDiscrepancies :many
-- Cameras where the NVR's reported channel status DISAGREES with the linked
-- standalone camera device's own reachability: the NVR says offline but the device
-- is up, or the NVR says online but the device is down. This is the "is the camera
-- REALLY online?" cross-check — two independent views contradicting each other,
-- which a single source (trusting the NVR alone, or the device alone) would miss.
SELECT ch.nvr_device_id, nvr.name AS nvr_name, ch.channel_no,
       COALESCE(host(ch.camera_ip),'')::text AS camera_ip,
       COALESCE(ch.camera_name,'')::text AS camera_name,
       ch.status AS nvr_status,
       cam.id AS camera_device_id, cam.status AS device_status
FROM nvr_channels ch
JOIN devices nvr ON nvr.id = ch.nvr_device_id AND nvr.deleted_at IS NULL
JOIN devices cam ON cam.id = ch.camera_device_id AND cam.deleted_at IS NULL
WHERE (ch.status = 'offline' AND cam.status = 'up')
   OR (ch.status = 'online'  AND cam.status = 'down')
ORDER BY nvr.name, ch.channel_no;

-- name: CountStaleNVRChannels :one
-- Channels whose status hasn't been refreshed since the cutoff — the NVR-side poll
-- couldn't reach/authenticate the recorder, so their online/offline is a stale
-- snapshot and must NOT be trusted as current. (The exact staleness that made a
-- down camera still read "online" before the channel monitor existed.)
SELECT count(*) FROM nvr_channels WHERE last_seen_at < $1;

-- name: FindNVRsForCamera :many
-- Path Finder: which NVR/DVR(s) record this camera device, with the channel +
-- recording status, so a camera's path shows the recorder it feeds.
SELECT ch.nvr_device_id, d.name AS nvr_name, COALESCE(host(d.primary_ip),'')::text AS nvr_ip,
       ch.channel_no, COALESCE(ch.status,'')::text AS status
FROM nvr_channels ch JOIN devices d ON d.id = ch.nvr_device_id AND d.deleted_at IS NULL
WHERE ch.camera_device_id = $1
ORDER BY d.name, ch.channel_no;

-- name: ListLinkedCameraDeviceIDs :many
-- Camera device_ids that are an NVR/DVR channel (recorded by a recorder). These
-- are managed VIA the recorder even when they expose no directly-authenticable
-- web/ONVIF interface (RTSP-only feeds) — so they must not show credential_failed.
SELECT DISTINCT camera_device_id FROM nvr_channels WHERE camera_device_id IS NOT NULL;

-- name: NVRChannelStats :one
-- Path Finder: per-NVR channel totals (and how many are linked to a camera device).
SELECT count(*)::bigint AS total, count(camera_device_id)::bigint AS linked
FROM nvr_channels WHERE nvr_device_id = $1;

-- name: UpsertNVRChannel :one
INSERT INTO nvr_channels (nvr_device_id, channel_no, camera_name, camera_ip, camera_device_id, status, enabled, recording, resolution, detect_reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (nvr_device_id, channel_no) DO UPDATE SET
    camera_name = EXCLUDED.camera_name,
    camera_ip = EXCLUDED.camera_ip,
    -- Sticky link: a re-collect that resolves a device repoints the link, but one
    -- that momentarily can't (transient lookup miss / race during a parallel scan)
    -- keeps the existing link instead of nulling it. Links are only added/repointed
    -- here; they are cleared only when the camera device is deleted (FK ON DELETE
    -- SET NULL). ReconcileNVRChannelLinks heals any that were never set.
    camera_device_id = COALESCE(EXCLUDED.camera_device_id, nvr_channels.camera_device_id),
    status = EXCLUDED.status,
    enabled = EXCLUDED.enabled,
    recording = EXCLUDED.recording,
    resolution = EXCLUDED.resolution,
    detect_reason = EXCLUDED.detect_reason,
    last_seen_at = now()
RETURNING *;

-- name: ReconcileNVRChannelLinks :execrows
-- Link every NVR/DVR channel to the live device at its camera_ip, so a channel
-- and the standalone camera device cross-reference regardless of the order they
-- were discovered/collected. The per-channel link is computed once at NVR-collect
-- time (persistNVR via LiveDeviceByIP), so a camera discovered AFTER its NVR was
-- collected — or one whose apply raced the NVR collect — would otherwise stay
-- unlinked forever. Idempotent + set-based: exact primary_ip match, never links a
-- channel to its own NVR, picks the most-recently-updated device when an IP
-- recurs. Device deletes clear links via the FK (ON DELETE SET NULL), so this
-- only ADDS/repoints. Returns the number of channels (re)linked.
UPDATE nvr_channels ch
SET camera_device_id = pick.id
FROM (
    SELECT DISTINCT ON (primary_ip) primary_ip, id
    FROM devices
    WHERE deleted_at IS NULL AND primary_ip IS NOT NULL
    ORDER BY primary_ip, updated_at DESC
) pick
WHERE ch.camera_ip IS NOT NULL
  AND pick.primary_ip = ch.camera_ip
  AND pick.id <> ch.nvr_device_id
  AND ch.camera_device_id IS DISTINCT FROM pick.id;

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
