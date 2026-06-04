-- ---- Interfaces -----------------------------------------------------------

-- name: UpsertInterface :one
INSERT INTO interfaces (
    device_id, if_index, if_name, if_descr, if_alias,
    if_type, mac, speed_mbps, admin_status, oper_status,
    port_role, collection_source, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (device_id, if_index) DO UPDATE SET
    if_name = EXCLUDED.if_name,
    if_descr = EXCLUDED.if_descr,
    if_alias = EXCLUDED.if_alias,
    if_type = EXCLUDED.if_type,
    mac = EXCLUDED.mac,
    speed_mbps = EXCLUDED.speed_mbps,
    admin_status = EXCLUDED.admin_status,
    oper_status = EXCLUDED.oper_status,
    port_role = EXCLUDED.port_role,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = now()
RETURNING *;

-- name: ListInterfaces :many
SELECT * FROM interfaces WHERE device_id = $1 ORDER BY if_index;

-- name: DeleteStaleInterfaces :exec
DELETE FROM interfaces
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- ---- VLANs ----------------------------------------------------------------

-- name: UpsertVlan :one
INSERT INTO vlans (device_id, vlan_id, name, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (device_id, vlan_id) DO UPDATE SET
    name = EXCLUDED.name,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at
RETURNING *;

-- name: ListVlans :many
SELECT * FROM vlans WHERE device_id = $1 ORDER BY vlan_id;

-- name: DeleteStaleVlans :exec
DELETE FROM vlans
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- name: UpsertPortVlan :exec
INSERT INTO port_vlans (device_id, if_index, vlan_id, tagged, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (device_id, if_index, vlan_id) DO UPDATE SET
    tagged = EXCLUDED.tagged,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListPortVlans :many
SELECT * FROM port_vlans WHERE device_id = $1 ORDER BY if_index, vlan_id;

-- name: DeleteStalePortVlans :exec
DELETE FROM port_vlans
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- ---- MAC address table ---------------------------------------------------

-- name: UpsertMAC :exec
INSERT INTO mac_addresses (device_id, mac, vlan_id, if_index, fdb_status, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (device_id, mac, vlan_id) DO UPDATE SET
    if_index = EXCLUDED.if_index,
    fdb_status = EXCLUDED.fdb_status,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleMACEntries :exec
DELETE FROM mac_addresses
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- name: FindMACOnSwitches :many
-- Topology search: which switch + port + VLAN carries a MAC?
SELECT m.mac, m.vlan_id, m.if_index, m.device_id,
       d.name AS device_name, d.primary_ip,
       i.if_name, i.port_role
FROM mac_addresses m
JOIN devices d ON d.id = m.device_id AND d.deleted_at IS NULL
LEFT JOIN interfaces i ON i.device_id = m.device_id AND i.if_index = m.if_index
WHERE m.mac = $1
ORDER BY m.last_seen_at DESC;

-- ---- ARP entries ---------------------------------------------------------

-- name: UpsertARP :exec
INSERT INTO arp_entries (device_id, ip_address, mac, if_index, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (device_id, ip_address, mac) DO UPDATE SET
    if_index = EXCLUDED.if_index,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleARP :exec
DELETE FROM arp_entries
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- name: FindMACByIP :many
-- First step of the IP→MAC→port→path search.
SELECT ip_address, mac, device_id, last_seen_at
FROM arp_entries
WHERE ip_address = $1
ORDER BY last_seen_at DESC
LIMIT 5;

-- ---- Neighbors (LLDP/CDP) -----------------------------------------------

-- name: UpsertNeighbor :one
INSERT INTO neighbors (
    device_id, local_if_index, local_if_name, rem_chassis_id,
    rem_sys_name, rem_sys_desc, rem_port_id, rem_port_desc, rem_mgmt_ip,
    protocol, collection_source, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (device_id, local_if_index, rem_chassis_id) DO UPDATE SET
    local_if_name = EXCLUDED.local_if_name,
    rem_sys_name = EXCLUDED.rem_sys_name,
    rem_sys_desc = EXCLUDED.rem_sys_desc,
    rem_port_id = EXCLUDED.rem_port_id,
    rem_port_desc = EXCLUDED.rem_port_desc,
    rem_mgmt_ip = EXCLUDED.rem_mgmt_ip,
    protocol = EXCLUDED.protocol,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at
RETURNING *;

-- name: ListNeighbors :many
SELECT * FROM neighbors WHERE device_id = $1 ORDER BY local_if_index;

-- name: DeleteStaleNeighbors :exec
DELETE FROM neighbors
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;

-- ---- Topology links ------------------------------------------------------

-- name: UpsertTopologyLink :exec
INSERT INTO topology_links (
    local_device_id, local_if_index, local_if_name,
    remote_device_id, remote_ip, remote_sys_name,
    link_source, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (local_device_id, local_if_index, remote_device_id) DO UPDATE SET
    local_if_name = EXCLUDED.local_if_name,
    remote_ip = EXCLUDED.remote_ip,
    remote_sys_name = EXCLUDED.remote_sys_name,
    link_source = EXCLUDED.link_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: ListTopologyLinks :many
SELECT * FROM topology_links WHERE local_device_id = $1 ORDER BY local_if_index;

-- name: ListAllTopologyLinks :many
-- Used by the topology graph to build the full picture.
SELECT tl.*,
       ld.name AS local_name, ld.primary_ip AS local_ip, ld.category AS local_category,
       rd.name AS remote_name, rd.primary_ip AS remote_ip_col, rd.category AS remote_category
FROM topology_links tl
JOIN devices ld ON ld.id = tl.local_device_id AND ld.deleted_at IS NULL
LEFT JOIN devices rd ON rd.id = tl.remote_device_id AND rd.deleted_at IS NULL
ORDER BY ld.name, tl.local_if_index;

-- ---- Read APIs for the device detail Ports / MAC / ARP tabs --------------

-- name: ListMACForDevice :many
-- The switch FDB with the local port name and, when the MAC belongs to a
-- known device interface, that owner device's name + vendor (real correlation,
-- no OUI guesswork).
SELECT m.id, m.mac, m.vlan_id, m.if_index, m.fdb_status,
       m.collection_source, m.last_seen_at,
       i.if_name AS if_name,
       owner.name AS owner_name,
       owner.vendor AS owner_vendor
FROM mac_addresses m
LEFT JOIN interfaces i ON i.device_id = m.device_id AND i.if_index = m.if_index
LEFT JOIN LATERAL (
    SELECT d.name, d.vendor
    FROM interfaces oi
    JOIN devices d ON d.id = oi.device_id AND d.deleted_at IS NULL
    WHERE oi.mac = m.mac AND oi.device_id <> m.device_id
    LIMIT 1
) owner ON true
WHERE m.device_id = $1
ORDER BY m.vlan_id, m.mac;

-- name: ListARPForDevice :many
SELECT a.id, a.ip_address, a.mac, a.if_index, a.collection_source, a.last_seen_at,
       i.if_name AS if_name,
       owner.name AS owner_name
FROM arp_entries a
LEFT JOIN interfaces i ON i.device_id = a.device_id AND i.if_index = a.if_index
LEFT JOIN LATERAL (
    SELECT d.name FROM interfaces oi
    JOIN devices d ON d.id = oi.device_id AND d.deleted_at IS NULL
    WHERE oi.mac = a.mac AND oi.device_id <> a.device_id
    LIMIT 1
) owner ON true
WHERE a.device_id = $1
ORDER BY a.ip_address;

-- name: MACCountByPort :many
SELECT if_index, COUNT(*) AS mac_count
FROM mac_addresses
WHERE device_id = $1 AND if_index IS NOT NULL
GROUP BY if_index;
