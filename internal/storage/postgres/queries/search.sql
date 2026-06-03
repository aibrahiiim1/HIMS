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
SET outcome = $2, device_id = $3, driver = $4, category = $5, error = $6
WHERE id = $1;

-- name: ListDiscoveryResults :many
SELECT * FROM discovery_results WHERE job_id = $1 ORDER BY probed_at DESC;

-- name: GetDiscoveryJob :one
SELECT * FROM discovery_jobs WHERE id = $1;

-- name: ListDiscoveryJobs :many
SELECT * FROM discovery_jobs ORDER BY created_at DESC LIMIT 50;

-- name: DeleteDiscoveryJob :exec
-- Removes a job and its results (discovery_results FK ON DELETE CASCADE).
DELETE FROM discovery_jobs WHERE id = $1;
