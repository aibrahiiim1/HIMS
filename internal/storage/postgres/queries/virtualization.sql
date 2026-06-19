-- name: ListVMsByHost :many
SELECT * FROM virtual_machines WHERE host_device_id = $1 ORDER BY name;

-- name: UpsertVM :one
-- Upsert keyed on (host, name): re-collecting refreshes state without dups. COALESCE on
-- vm_id/mac so a later collection that lacks them (e.g. vSphere) never wipes Hyper-V values.
INSERT INTO virtual_machines (host_device_id, vm_device_id, name, power_state, vcpu, mem_mb, guest_os, primary_ip, vm_id, mac)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (host_device_id, name) DO UPDATE SET
    vm_device_id = EXCLUDED.vm_device_id,
    power_state = EXCLUDED.power_state,
    vcpu = EXCLUDED.vcpu,
    mem_mb = EXCLUDED.mem_mb,
    guest_os = EXCLUDED.guest_os,
    primary_ip = EXCLUDED.primary_ip,
    vm_id = COALESCE(EXCLUDED.vm_id, virtual_machines.vm_id),
    mac = COALESCE(EXCLUDED.mac, virtual_machines.mac),
    last_seen_at = now()
RETURNING *;

-- name: DeviceIDByMAC :one
-- A discovered (non-virtual) device whose collected NIC MAC matches, normalized so
-- colon/dash/case differences don't matter — used to reverse-link a Hyper-V guest VM to
-- an existing device by MAC when its guest IP is unavailable (no integration services).
SELECT n.device_id FROM os_nics n
JOIN devices d ON d.id = n.device_id AND d.deleted_at IS NULL AND d.is_virtual = false AND d.category <> 'virtual_host'
WHERE n.mac <> '' AND lower(regexp_replace(n.mac,'[^0-9A-Fa-f]','','g')) = lower(regexp_replace(@mac::text,'[^0-9A-Fa-f]','','g'))
LIMIT 1;

-- name: DeviceHypervisorTypes :many
-- Per-device hypervisor.type fact (esxi/hyperv) — feeds the derived server_role so the UI
-- can distinguish an ESXi host from a Hyper-V host without re-reading every device's facts.
SELECT device_id, value FROM device_facts
WHERE key = 'hypervisor.type' AND value IS NOT NULL;

-- name: LinkedVMParents :many
-- Every VM that is linked to a discovered device, with its parent host — so a device that IS
-- a VM derives server_role=virtual_machine and shows "hosted on <host>" (reverse link).
SELECT vm.vm_device_id, vm.host_device_id, h.name AS host_name, h.primary_ip AS host_ip
FROM virtual_machines vm JOIN devices h ON h.id = vm.host_device_id
WHERE vm.vm_device_id IS NOT NULL;

-- ===== Stage 2: rich virtualization detail (durable tables) =====

-- name: UpsertDatastore :exec
INSERT INTO vh_datastores (host_device_id, name, type, capacity_bytes, free_bytes)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (host_device_id, name) DO UPDATE SET
  type=EXCLUDED.type, capacity_bytes=EXCLUDED.capacity_bytes, free_bytes=EXCLUDED.free_bytes, last_seen_at=now();

-- name: ListDatastoresByHost :many
SELECT * FROM vh_datastores WHERE host_device_id=$1 ORDER BY name;

-- name: DeleteStaleDatastores :exec
DELETE FROM vh_datastores WHERE host_device_id=$1 AND last_seen_at < $2;

-- name: UpsertVHNetwork :exec
INSERT INTO vh_networks (host_device_id, kind, name, vlan, uplinks, switch_name)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (host_device_id, kind, name) DO UPDATE SET
  vlan=EXCLUDED.vlan, uplinks=EXCLUDED.uplinks, switch_name=EXCLUDED.switch_name, last_seen_at=now();

-- name: ListNetworksByHost :many
SELECT * FROM vh_networks WHERE host_device_id=$1 ORDER BY kind, name;

-- name: DeleteStaleNetworks :exec
DELETE FROM vh_networks WHERE host_device_id=$1 AND last_seen_at < $2;

-- name: UpsertHostNic :exec
INSERT INTO vh_host_nics (host_device_id, name, mac, link_speed_mbps, link_up)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (host_device_id, name) DO UPDATE SET
  mac=EXCLUDED.mac, link_speed_mbps=EXCLUDED.link_speed_mbps, link_up=EXCLUDED.link_up, last_seen_at=now();

-- name: ListHostNicsByHost :many
SELECT * FROM vh_host_nics WHERE host_device_id=$1 ORDER BY name;

-- name: DeleteStaleHostNics :exec
DELETE FROM vh_host_nics WHERE host_device_id=$1 AND last_seen_at < $2;

-- name: UpsertVMDisk :exec
INSERT INTO vm_disks (vm_id, label, path, datastore, capacity_bytes, used_bytes)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (vm_id, label) DO UPDATE SET
  path=EXCLUDED.path, datastore=EXCLUDED.datastore, capacity_bytes=EXCLUDED.capacity_bytes, used_bytes=EXCLUDED.used_bytes, last_seen_at=now();

-- name: ListVMDisksByVM :many
SELECT * FROM vm_disks WHERE vm_id=$1 ORDER BY label;

-- name: DeleteStaleVMDisks :exec
DELETE FROM vm_disks WHERE vm_id=$1 AND last_seen_at < $2;

-- name: UpsertVMNic :exec
INSERT INTO vm_nics (vm_id, mac, network, ip_addresses, connected)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (vm_id, mac) DO UPDATE SET
  network=EXCLUDED.network, ip_addresses=EXCLUDED.ip_addresses, connected=EXCLUDED.connected, last_seen_at=now();

-- name: ListVMNicsByVM :many
SELECT * FROM vm_nics WHERE vm_id=$1 ORDER BY mac;

-- name: DeleteStaleVMNics :exec
DELETE FROM vm_nics WHERE vm_id=$1 AND last_seen_at < $2;

-- name: UpsertCollectionHealth :exec
INSERT INTO vh_collection_health (device_id, collector, status, detail, vm_count)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (device_id, collector) DO UPDATE SET
  status=EXCLUDED.status, detail=EXCLUDED.detail, vm_count=EXCLUDED.vm_count, collected_at=now();

-- name: GetCollectionHealth :many
SELECT * FROM vh_collection_health WHERE device_id=$1 ORDER BY collector;

-- name: SetVMExtra :exec
-- Platform-specific extra VM attributes; COALESCE(NULLIF...) so a collector that doesn't
-- supply a field (e.g. vSphere has no generation) never wipes another platform's value.
UPDATE virtual_machines SET
  tools_state          = COALESCE(NULLIF(@tools_state::text,''), tools_state),
  generation           = COALESCE(NULLIF(@generation::text,''), generation),
  datastore            = COALESCE(NULLIF(@datastore::text,''), datastore),
  integration_services = COALESCE(NULLIF(@integration_services::text,''), integration_services),
  uptime_seconds       = COALESCE(NULLIF(@uptime_seconds::bigint,0), uptime_seconds),
  mem_used_mb          = COALESCE(NULLIF(@mem_used_mb::int,0), mem_used_mb)
WHERE id = @id;

-- name: VMCountsByHost :many
SELECT host_device_id,
  count(*)::int AS total,
  count(*) FILTER (WHERE power_state='on')::int  AS running,
  count(*) FILTER (WHERE power_state='off')::int AS stopped
FROM virtual_machines GROUP BY host_device_id;

-- name: ListVMsByHostDetail :many
SELECT vm.*, ld.name AS linked_name, (COALESCE(host(ld.primary_ip)::text,''))::text AS linked_ip
FROM virtual_machines vm LEFT JOIN devices ld ON ld.id = vm.vm_device_id
WHERE vm.host_device_id = $1 ORDER BY vm.name;

-- name: ListVMDisksByHost :many
SELECT vd.* FROM vm_disks vd JOIN virtual_machines vm ON vm.id = vd.vm_id WHERE vm.host_device_id = $1;

-- name: ListVMNicsByHost :many
SELECT vn.* FROM vm_nics vn JOIN virtual_machines vm ON vm.id = vn.vm_id WHERE vm.host_device_id = $1;

-- name: DatastoreSummaryByHost :many
SELECT host_device_id, count(*)::int AS n,
  COALESCE(sum(capacity_bytes),0)::bigint AS capacity, COALESCE(sum(free_bytes),0)::bigint AS free
FROM vh_datastores GROUP BY host_device_id;

-- name: NetworkCountByHost :many
SELECT host_device_id, count(*)::int AS n FROM vh_networks GROUP BY host_device_id;

-- name: VMLinkSummary :one
SELECT count(*)::int AS total, count(vm_device_id)::int AS linked FROM virtual_machines;
