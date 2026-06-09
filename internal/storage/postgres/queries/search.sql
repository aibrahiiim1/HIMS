-- name: SearchByIP :one
-- Primary search entry point for the IP → MAC → port path resolution.
SELECT id, name, primary_ip, hostname, category, status, location_id
FROM devices
WHERE primary_ip = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: SearchByHostname :many
SELECT id, name, primary_ip, hostname, category, status, location_id
FROM devices
WHERE (hostname ILIKE $1 OR name ILIKE $1) AND deleted_at IS NULL
ORDER BY name
LIMIT 20;

-- name: SearchByMAC :many
-- Finds the switch(es) that have this MAC in their FDB, then joins the
-- interface for port + VLAN detail.
SELECT
    m.mac, m.vlan_id, m.if_index, m.last_seen_at,
    d.id AS device_id, d.name AS switch_name, d.primary_ip AS switch_ip,
    i.if_name, i.if_alias, i.port_role,
    a.ip_address AS known_ip
FROM mac_addresses m
JOIN devices d ON d.id = m.device_id AND d.deleted_at IS NULL
LEFT JOIN interfaces i ON i.device_id = m.device_id AND i.if_index = m.if_index
LEFT JOIN arp_entries a ON a.mac = m.mac
WHERE m.mac = $1
ORDER BY m.last_seen_at DESC;

-- name: SearchAccessPoints :many
-- Global-search: access points by name / MAC / IP / serial / model. Returns the
-- owning controller so an AP MAC or IP found anywhere resolves to a device.
SELECT ap.controller_device_id, d.name AS controller_name, d.category AS controller_category,
       ap.name, ap.mac, ap.model, host(ap.ip)::text AS ip, ap.site, ap.status
FROM access_points ap
LEFT JOIN devices d ON d.id = ap.controller_device_id AND d.deleted_at IS NULL
WHERE ap.name ILIKE '%'||$1||'%'
   OR COALESCE(ap.mac,'') ILIKE '%'||$1||'%'
   OR COALESCE(ap.serial,'') ILIKE '%'||$1||'%'
   OR COALESCE(ap.model,'') ILIKE '%'||$1||'%'
   OR COALESCE(host(ap.ip),'') ILIKE '%'||$1||'%'
ORDER BY ap.name
LIMIT 30;

-- name: SearchWirelessClients :many
-- Global-search: associated wireless clients by MAC / IP / hostname / SSID / AP.
SELECT wc.controller_device_id, d.name AS controller_name, d.category AS controller_category,
       wc.mac, wc.ip, wc.hostname, wc.ap_name, wc.ssid, wc.band
FROM wireless_clients wc
LEFT JOIN devices d ON d.id = wc.controller_device_id AND d.deleted_at IS NULL
WHERE wc.mac ILIKE '%'||$1||'%'
   OR wc.ip ILIKE '%'||$1||'%'
   OR wc.hostname ILIKE '%'||$1||'%'
   OR wc.ssid ILIKE '%'||$1||'%'
   OR wc.ap_name ILIKE '%'||$1||'%'
ORDER BY wc.hostname, wc.mac
LIMIT 30;

-- name: SearchFdbMacs :many
-- Global-search: learned MAC addresses (bridge FDB) by MAC — which switch + port
-- a MAC was seen on, anywhere in the fabric.
SELECT m.device_id, d.name AS device_name, d.category AS device_category,
       m.mac, m.vlan_id, m.if_index, i.if_name, i.if_alias, m.last_seen_at
FROM mac_addresses m
JOIN devices d ON d.id = m.device_id AND d.deleted_at IS NULL
LEFT JOIN interfaces i ON i.device_id = m.device_id AND i.if_index = m.if_index
WHERE m.mac ILIKE '%'||$1||'%'
ORDER BY m.last_seen_at DESC
LIMIT 40;

-- name: SearchArpEntries :many
-- Global-search: ARP table (IP↔MAC) by IP or MAC — which L3 device resolved an IP.
SELECT a.device_id, d.name AS device_name, d.category AS device_category,
       host(a.ip_address)::text AS ip, a.mac, a.if_index, a.last_seen_at
FROM arp_entries a
JOIN devices d ON d.id = a.device_id AND d.deleted_at IS NULL
WHERE host(a.ip_address) ILIKE '%'||$1||'%' OR a.mac ILIKE '%'||$1||'%'
ORDER BY a.last_seen_at DESC
LIMIT 40;

-- name: ResolveIPToMAC :many
-- Path Finder fallback for when the ARP table is empty/sparse: resolve an IP to a
-- MAC (and an identity) from the wireless-client roster and the AP inventory, so a
-- wireless endpoint's IP still traces to its switch port via the FDB. Clients are
-- preferred over APs ($1 = IP as text).
SELECT mac, source, device_id, device_name
FROM (
    SELECT wc.mac AS mac,
           'wireless_client'::text AS source,
           wc.controller_device_id AS device_id,
           COALESCE(NULLIF(wc.hostname, ''), wc.mac) AS device_name,
           1 AS pri
    FROM wireless_clients wc
    WHERE wc.ip = $1 AND wc.mac <> ''
    UNION ALL
    SELECT ap.mac AS mac,
           'access_point'::text AS source,
           ap.controller_device_id AS device_id,
           ap.name AS device_name,
           2 AS pri
    FROM access_points ap
    WHERE host(ap.ip) = $1 AND ap.mac IS NOT NULL
) x
ORDER BY pri
LIMIT 5;

-- name: CreateDiscoveryJob :one
INSERT INTO discovery_jobs (location_id, subnet_id, scope_cidr, status)
VALUES ($1, $2, $3, 'pending')
RETURNING *;

-- name: SetDiscoveryJobMetadata :exec
-- Stores the scan spec (mode/targets/creds) so the job can be re-run as-is.
UPDATE discovery_jobs SET metadata = $2 WHERE id = $1;

-- name: UpdateDiscoveryJobStatus :exec
UPDATE discovery_jobs
SET status = $2,
    started_at    = CASE WHEN $2 = 'running' THEN now() ELSE started_at END,
    finished_at   = CASE WHEN $2 IN ('completed','failed','cancelled') THEN now() ELSE finished_at END,
    host_count    = COALESCE($3, host_count),
    found_count   = COALESCE($4, found_count),
    scanned_count = COALESCE($6, scanned_count),
    error         = $5
WHERE id = $1;

-- name: IncrDiscoveryJobScanned :exec
-- Atomically bump the per-host scan progress counter (safe under the concurrent
-- per-host workers). Drives the running-scan 0→100% progress bar.
UPDATE discovery_jobs SET scanned_count = scanned_count + 1 WHERE id = $1;

-- name: CreateDiscoveryResult :one
INSERT INTO discovery_results (job_id, ip, outcome, probe_data)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateDiscoveryResult :exec
UPDATE discovery_results
SET outcome = $2, device_id = $3, driver = $4, category = $5, error = $6, probe_data = $7,
    disposition = $8, retry_count = $9
WHERE id = $1;

-- name: ListDiscoveryResults :many
SELECT * FROM discovery_results WHERE job_id = $1 ORDER BY probed_at DESC;

-- name: ListKnownDeviceScanDispositions :many
-- Per-device scan dispositions across recent jobs (newest first). Powers the
-- scan-stability Data Quality issues: missed-last-scan, flapping (recovered by
-- retry), and frequently-missed-known-device. Bounded so the scan history of a
-- long-lived deployment cannot blow up the query.
SELECT device_id, disposition, job_id, probed_at
FROM discovery_results
WHERE device_id IS NOT NULL
  AND disposition IN ('known_seen','known_recovered','known_missed','known_unreachable')
ORDER BY probed_at DESC
LIMIT 5000;

-- name: LatestDeviceProbeData :one
-- The most recent scan probe_data for a device (open ports, evidence, etc.) —
-- used to repair its reachability check from the ports it actually answered on.
SELECT probe_data FROM discovery_results
WHERE device_id = $1 ORDER BY probed_at DESC LIMIT 1;

-- name: GetDiscoveryJob :one
SELECT * FROM discovery_jobs WHERE id = $1;

-- name: ListDiscoveryJobs :many
SELECT * FROM discovery_jobs ORDER BY created_at DESC LIMIT 50;

-- name: DeleteDiscoveryJob :exec
-- Removes a job and its results (discovery_results FK ON DELETE CASCADE).
DELETE FROM discovery_jobs WHERE id = $1;

-- name: CreateDiscoveryJobEvent :one
-- Persist one live-discovery event for completed-job playback (the live SSE feed
-- is served from the in-memory hub; this is the durable history).
INSERT INTO discovery_job_events (job_id, ip, device_id, stage, protocol, status, message)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING seq;

-- name: ListDiscoveryJobEvents :many
SELECT seq, job_id, ip, device_id, stage, protocol, status, message, created_at
FROM discovery_job_events WHERE job_id = $1 ORDER BY seq ASC LIMIT 5000;

-- name: FailStaleScanJobs :execrows
-- Reconcile orphaned scans: a scan runs as an in-process goroutine, so any job
-- still 'running'/'pending' after a restart (or that hangs past a max duration)
-- has no live worker and must be failed. $1 = cutoff (started/created before
-- this), $2 = error message. Returns the number of jobs reconciled.
UPDATE discovery_jobs
SET status = 'failed',
    finished_at = now(),
    error = $2
WHERE status IN ('running', 'pending')
  AND COALESCE(started_at, created_at) < $1;
