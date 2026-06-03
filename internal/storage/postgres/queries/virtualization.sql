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
