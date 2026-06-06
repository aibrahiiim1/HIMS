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
    started_at   = CASE WHEN $2 = 'running' THEN now() ELSE started_at END,
    finished_at  = CASE WHEN $2 IN ('completed','failed','cancelled') THEN now() ELSE finished_at END,
    host_count   = COALESCE($3, host_count),
    found_count  = COALESCE($4, found_count),
    error        = $5
WHERE id = $1;

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
