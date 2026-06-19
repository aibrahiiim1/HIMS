-- name: ListVMsByHost :many
SELECT * FROM virtual_machines WHERE host_device_id = $1 ORDER BY name;

-- name: UpsertVM :one
-- Upsert keyed on (host, name): re-collecting refreshes state without dups.
INSERT INTO virtual_machines (host_device_id, vm_device_id, name, power_state, vcpu, mem_mb, guest_os, primary_ip)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (host_device_id, name) DO UPDATE SET
    vm_device_id = EXCLUDED.vm_device_id,
    power_state = EXCLUDED.power_state,
    vcpu = EXCLUDED.vcpu,
    mem_mb = EXCLUDED.mem_mb,
    guest_os = EXCLUDED.guest_os,
    primary_ip = EXCLUDED.primary_ip,
    last_seen_at = now()
RETURNING *;

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
