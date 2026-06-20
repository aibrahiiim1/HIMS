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

-- name: DeleteStaleTopologyLinks :execrows
-- Prune links not re-seen since the cutoff (a neighbor that stopped reporting).
DELETE FROM topology_links WHERE last_seen_at < $1;

-- ---- Read APIs for the device detail Ports / MAC / ARP tabs --------------

-- name: ListMACForDevice :many
-- The switch FDB with the local port name and, when the MAC belongs to a
-- known device interface, that owner device's name + vendor (real correlation,
-- no OUI guesswork).
SELECT m.id, m.mac, m.vlan_id, m.if_index, m.fdb_status,
       m.collection_source, m.last_seen_at,
       i.if_name AS if_name,
       COALESCE(owner.name, '') AS owner_name,
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
       COALESCE(owner.name, '') AS owner_name
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

-- name: MaxNeighborSeenAt :one
-- Freshness of the most recent LLDP/CDP neighbor observation (topology age).
SELECT MAX(last_seen_at)::timestamptz AS max_seen FROM neighbors;

-- name: ListFabricInterfaceMACs :many
-- Every interface MAC belonging to a topology-capable fabric device (switch /
-- router / ISP router). Used to map an observed FDB MAC back to the device that
-- owns it, for vendor-neutral L2 link inference (FDB-based topology).
SELECT i.device_id, i.mac
FROM interfaces i
JOIN devices d ON d.id = i.device_id AND d.deleted_at IS NULL
WHERE i.mac IS NOT NULL AND i.mac <> ''
  AND d.category IN ('switch', 'router', 'isp_router');

-- name: ListDeviceLinksBidirectional :many
-- All topology links touching a device from EITHER endpoint, with both ends
-- enriched (name/ip/vendor/category) and an `inbound` flag set when the device
-- is the link's remote side. The handler normalizes this so the per-device
-- Topology tab always shows the OTHER device — including links that point AT it
-- (e.g. a MAC/FDB-derived cross-vendor uplink stored from the neighbour's side).
SELECT tl.id,
       tl.local_device_id, tl.local_if_index, tl.local_if_name,
       tl.remote_device_id, tl.remote_sys_name, tl.remote_ip,
       tl.link_source, tl.last_seen_at,
       (tl.local_device_id <> $1) AS inbound,
       ld.name AS local_name, ld.primary_ip AS local_dev_ip, ld.vendor AS local_vendor, ld.category AS local_category,
       rd.name AS remote_name, rd.primary_ip AS remote_dev_ip, rd.vendor AS remote_vendor, rd.category AS remote_category
FROM topology_links tl
JOIN devices ld ON ld.id = tl.local_device_id AND ld.deleted_at IS NULL
LEFT JOIN devices rd ON rd.id = tl.remote_device_id AND rd.deleted_at IS NULL
WHERE tl.local_device_id = $1 OR tl.remote_device_id = $1
ORDER BY tl.local_if_index NULLS LAST;

-- name: ListUnknownMACs :many
-- MACs learned in a switch FDB that map to NO inventory device: not a known device
-- NIC (interfaces/os_nics/vm_nics) AND whose ARP-derived IP (if any) is not an
-- inventory device's primary IP. One row per (mac) — the EDGE port (fewest MACs)
-- where it was seen — with the switch, port, VLAN, last-seen and a possible IP from
-- ARP. No fake devices are created; this is pure visibility into unmapped endpoints.
SELECT DISTINCT ON (m.mac)
  m.mac,
  m.device_id            AS switch_id,
  sw.name                AS switch_name,
  m.if_index,
  i.if_name,
  i.if_alias,
  m.vlan_id,
  m.last_seen_at,
  cnt.mac_count          AS port_mac_count,
  arp.ip_address         AS possible_ip
FROM mac_addresses m
JOIN devices sw ON sw.id = m.device_id AND sw.deleted_at IS NULL
LEFT JOIN interfaces i ON i.device_id = m.device_id AND i.if_index = m.if_index
LEFT JOIN LATERAL (
  SELECT count(*) AS mac_count FROM mac_addresses mm
  WHERE mm.device_id = m.device_id AND mm.if_index = m.if_index
) cnt ON true
LEFT JOIN LATERAL (
  SELECT a.ip_address FROM arp_entries a WHERE a.mac = m.mac ORDER BY a.last_seen_at DESC LIMIT 1
) arp ON true
WHERE NOT EXISTS (SELECT 1 FROM interfaces oi WHERE oi.mac = m.mac)
  AND NOT EXISTS (SELECT 1 FROM os_nics o   WHERE o.mac = m.mac)
  AND NOT EXISTS (SELECT 1 FROM vm_nics v   WHERE v.mac = m.mac)
  AND NOT EXISTS (
    SELECT 1 FROM arp_entries a2 JOIN devices d2 ON d2.primary_ip = a2.ip_address AND d2.deleted_at IS NULL
    WHERE a2.mac = m.mac
  )
ORDER BY m.mac, cnt.mac_count ASC NULLS LAST, m.last_seen_at DESC;

-- name: CountUnknownMACs :one
SELECT count(DISTINCT m.mac)::int AS total
FROM mac_addresses m
WHERE NOT EXISTS (SELECT 1 FROM interfaces oi WHERE oi.mac = m.mac)
  AND NOT EXISTS (SELECT 1 FROM os_nics o   WHERE o.mac = m.mac)
  AND NOT EXISTS (SELECT 1 FROM vm_nics v   WHERE v.mac = m.mac)
  AND NOT EXISTS (
    SELECT 1 FROM arp_entries a2 JOIN devices d2 ON d2.primary_ip = a2.ip_address AND d2.deleted_at IS NULL
    WHERE a2.mac = m.mac
  );

-- name: ResolvePortMap :many
-- Per FDB entry on a switch, resolve the learned MAC to an inventory device (by switch-interface
-- MAC, by OS NIC, or by ARP-derived IP), to a VM (by vNIC MAC), and to an IP (latest ARP). Each
-- candidate is returned separately and NULLable; the API coalesces with a priority + confidence.
-- No fake devices: a NULL device_id means the MAC is unmapped.
SELECT
  m.if_index, i.if_name, m.mac, m.vlan_id, m.last_seen_at,
  dev_if.id AS dev_if_id, COALESCE(dev_if.name,'') AS dev_if_name, COALESCE(dev_if.category,'') AS dev_if_cat,
  dev_nic.id AS dev_nic_id, COALESCE(dev_nic.name,'') AS dev_nic_name, COALESCE(dev_nic.category,'') AS dev_nic_cat,
  dev_arp.id AS dev_arp_id, COALESCE(dev_arp.name,'') AS dev_arp_name, COALESCE(dev_arp.category,'') AS dev_arp_cat,
  vm.id AS vm_id, COALESCE(vm.name,'') AS vm_name, vm.host_device_id AS vm_host_device_id, vm.vm_device_id AS vm_device_id,
  arp.ip_address AS resolved_ip
FROM mac_addresses m
LEFT JOIN interfaces i ON i.device_id = m.device_id AND i.if_index = m.if_index
LEFT JOIN LATERAL (
  SELECT d.id, d.name, d.category FROM interfaces oi JOIN devices d ON d.id = oi.device_id AND d.deleted_at IS NULL
  WHERE oi.mac = m.mac AND oi.device_id <> m.device_id LIMIT 1) dev_if ON true
LEFT JOIN LATERAL (
  SELECT d.id, d.name, d.category FROM os_nics o JOIN devices d ON d.id = o.device_id AND d.deleted_at IS NULL
  WHERE o.mac = m.mac LIMIT 1) dev_nic ON true
LEFT JOIN LATERAL (
  SELECT a.ip_address FROM arp_entries a WHERE a.mac = m.mac ORDER BY a.last_seen_at DESC LIMIT 1) arp ON true
LEFT JOIN LATERAL (
  SELECT d.id, d.name, d.category FROM devices d
  WHERE host(d.primary_ip) = host(arp.ip_address) AND d.deleted_at IS NULL LIMIT 1) dev_arp ON true
LEFT JOIN LATERAL (
  SELECT v.id, v.name, v.host_device_id, v.vm_device_id FROM vm_nics vn JOIN virtual_machines v ON v.id = vn.vm_id
  WHERE vn.mac = m.mac LIMIT 1) vm ON true
WHERE m.device_id = $1
ORDER BY m.if_index, m.mac;
