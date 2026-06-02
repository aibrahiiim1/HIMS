-- name: UpsertFirewallStatus :exec
INSERT INTO firewall_status (
    device_id, ha_mode, ha_group_name, ha_member_count, session_count,
    collection_source, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (device_id) DO UPDATE SET
    ha_mode = EXCLUDED.ha_mode,
    ha_group_name = EXCLUDED.ha_group_name,
    ha_member_count = EXCLUDED.ha_member_count,
    session_count = EXCLUDED.session_count,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = now();

-- name: GetFirewallStatus :one
SELECT * FROM firewall_status WHERE device_id = $1;

-- name: UpsertVpnTunnel :exec
INSERT INTO firewall_vpn_tunnels (
    device_id, tunnel_name, p1_name, remote_gw, status,
    in_octets, out_octets, collection_source, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (device_id, tunnel_name) DO UPDATE SET
    p1_name = EXCLUDED.p1_name,
    remote_gw = EXCLUDED.remote_gw,
    status = EXCLUDED.status,
    in_octets = EXCLUDED.in_octets,
    out_octets = EXCLUDED.out_octets,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = now();

-- name: ListVpnTunnels :many
SELECT * FROM firewall_vpn_tunnels WHERE device_id = $1 ORDER BY tunnel_name;

-- name: DeleteStaleVpnTunnels :exec
DELETE FROM firewall_vpn_tunnels
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- name: UpsertHAMember :exec
INSERT INTO firewall_ha_members (
    device_id, serial, hostname, cpu_pct, mem_pct, session_count,
    sync_status, collection_source, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (device_id, serial) DO UPDATE SET
    hostname = EXCLUDED.hostname,
    cpu_pct = EXCLUDED.cpu_pct,
    mem_pct = EXCLUDED.mem_pct,
    session_count = EXCLUDED.session_count,
    sync_status = EXCLUDED.sync_status,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = now();

-- name: ListHAMembers :many
SELECT * FROM firewall_ha_members WHERE device_id = $1 ORDER BY serial;

-- name: DeleteStaleHAMembers :exec
DELETE FROM firewall_ha_members
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- name: UpsertLicense :exec
INSERT INTO firewall_licenses (device_id, contract, expiry, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (device_id, contract) DO UPDATE SET
    expiry = EXCLUDED.expiry,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = now();

-- name: ListLicenses :many
SELECT * FROM firewall_licenses WHERE device_id = $1 ORDER BY contract;

-- name: DeleteStaleLicenses :exec
DELETE FROM firewall_licenses
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;
