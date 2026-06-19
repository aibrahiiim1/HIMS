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
