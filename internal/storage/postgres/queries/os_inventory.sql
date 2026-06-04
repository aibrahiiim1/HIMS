-- Deep OS Inventory queries. The 1:1 summary is upserted per device; the 1:N
-- collections follow the prune-on-poll pattern (Upsert all rows with last_seen_at
-- = poll, then DeleteStale* removes rows from the same source not seen this poll).

-- name: GetOSInventory :one
SELECT * FROM os_inventory WHERE device_id = $1;

-- name: UpsertOSInventory :one
INSERT INTO os_inventory (
    device_id, collection_method, collected_at,
    hostname, fqdn, domain, workgroup, logged_on_user, ad_distinguished_name, ad_ou_path,
    os_caption, os_version, os_build, os_edition, os_arch, kernel,
    install_date, last_boot, uptime_seconds, timezone,
    manufacturer, model, serial, asset_tag, bios_version, bios_date,
    cpu_model, cpu_sockets, cpu_cores, ram_total_bytes, ram_slots, swap_total_bytes,
    events_critical_24h, events_error_24h, events_warning_24h, last_critical_event,
    updated_at
) VALUES (
    $1, $2, now(),
    $3, $4, $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15,
    $16, $17, $18, $19,
    $20, $21, $22, $23, $24, $25,
    $26, $27, $28, $29, $30, $31,
    $32, $33, $34, $35,
    now()
)
ON CONFLICT (device_id) DO UPDATE SET
    collection_method = EXCLUDED.collection_method,
    collected_at = now(),
    hostname = EXCLUDED.hostname, fqdn = EXCLUDED.fqdn, domain = EXCLUDED.domain,
    workgroup = EXCLUDED.workgroup, logged_on_user = EXCLUDED.logged_on_user,
    ad_distinguished_name = EXCLUDED.ad_distinguished_name, ad_ou_path = EXCLUDED.ad_ou_path,
    os_caption = EXCLUDED.os_caption, os_version = EXCLUDED.os_version, os_build = EXCLUDED.os_build,
    os_edition = EXCLUDED.os_edition, os_arch = EXCLUDED.os_arch, kernel = EXCLUDED.kernel,
    install_date = EXCLUDED.install_date, last_boot = EXCLUDED.last_boot,
    uptime_seconds = EXCLUDED.uptime_seconds, timezone = EXCLUDED.timezone,
    manufacturer = EXCLUDED.manufacturer, model = EXCLUDED.model, serial = EXCLUDED.serial,
    asset_tag = EXCLUDED.asset_tag, bios_version = EXCLUDED.bios_version, bios_date = EXCLUDED.bios_date,
    cpu_model = EXCLUDED.cpu_model, cpu_sockets = EXCLUDED.cpu_sockets, cpu_cores = EXCLUDED.cpu_cores,
    ram_total_bytes = EXCLUDED.ram_total_bytes, ram_slots = EXCLUDED.ram_slots,
    swap_total_bytes = EXCLUDED.swap_total_bytes,
    events_critical_24h = EXCLUDED.events_critical_24h, events_error_24h = EXCLUDED.events_error_24h,
    events_warning_24h = EXCLUDED.events_warning_24h, last_critical_event = EXCLUDED.last_critical_event,
    updated_at = now()
RETURNING *;

-- name: ListDevicesWithoutOSInventory :many
-- Credentialed device classes (server/endpoint) that have never been OS-inventoried.
SELECT d.id, d.name, d.primary_ip, d.category, d.os_family
FROM devices d
LEFT JOIN os_inventory oi ON oi.device_id = d.id
WHERE d.deleted_at IS NULL AND oi.device_id IS NULL
  AND d.category IN ('server','endpoint')
ORDER BY d.name;

-- --- disks ---
-- name: ListOSDisks :many
SELECT * FROM os_disks WHERE device_id = $1 ORDER BY name;

-- name: UpsertOSDisk :exec
INSERT INTO os_disks (device_id, name, model, serial, filesystem, size_bytes, total_bytes, free_bytes, health, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (device_id, name) DO UPDATE SET
    model = EXCLUDED.model, serial = EXCLUDED.serial, filesystem = EXCLUDED.filesystem,
    size_bytes = EXCLUDED.size_bytes, total_bytes = EXCLUDED.total_bytes, free_bytes = EXCLUDED.free_bytes,
    health = EXCLUDED.health, collection_source = EXCLUDED.collection_source, last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleOSDisks :exec
DELETE FROM os_disks WHERE device_id = $1 AND collection_source = $2 AND last_seen_at < $3;

-- --- nics ---
-- name: ListOSNics :many
SELECT * FROM os_nics WHERE device_id = $1 ORDER BY name;

-- name: UpsertOSNic :exec
INSERT INTO os_nics (device_id, name, mac, ip_addresses, gateway, dns_servers, dhcp_enabled, link_speed_mbps, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (device_id, name) DO UPDATE SET
    mac = EXCLUDED.mac, ip_addresses = EXCLUDED.ip_addresses, gateway = EXCLUDED.gateway,
    dns_servers = EXCLUDED.dns_servers, dhcp_enabled = EXCLUDED.dhcp_enabled,
    link_speed_mbps = EXCLUDED.link_speed_mbps, collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleOSNics :exec
DELETE FROM os_nics WHERE device_id = $1 AND collection_source = $2 AND last_seen_at < $3;

-- --- services ---
-- name: ListOSServices :many
SELECT * FROM os_services WHERE device_id = $1 ORDER BY name;

-- name: UpsertOSService :exec
INSERT INTO os_services (device_id, name, display_name, status, start_type, account, description, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (device_id, name) DO UPDATE SET
    display_name = EXCLUDED.display_name, status = EXCLUDED.status, start_type = EXCLUDED.start_type,
    account = EXCLUDED.account, description = EXCLUDED.description,
    collection_source = EXCLUDED.collection_source, last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleOSServices :exec
DELETE FROM os_services WHERE device_id = $1 AND collection_source = $2 AND last_seen_at < $3;

-- --- processes ---
-- name: ListOSProcesses :many
SELECT * FROM os_processes WHERE device_id = $1 ORDER BY mem_bytes DESC NULLS LAST;

-- name: UpsertOSProcess :exec
INSERT INTO os_processes (device_id, pid, name, cpu_pct, mem_bytes, start_time, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (device_id, pid) DO UPDATE SET
    name = EXCLUDED.name, cpu_pct = EXCLUDED.cpu_pct, mem_bytes = EXCLUDED.mem_bytes,
    start_time = EXCLUDED.start_time, collection_source = EXCLUDED.collection_source, last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleOSProcesses :exec
DELETE FROM os_processes WHERE device_id = $1 AND collection_source = $2 AND last_seen_at < $3;

-- --- software ---
-- name: ListOSSoftware :many
SELECT * FROM os_software WHERE device_id = $1 ORDER BY name, version;

-- name: UpsertOSSoftware :exec
INSERT INTO os_software (device_id, name, version, publisher, arch, install_date, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (device_id, name, version) DO UPDATE SET
    publisher = EXCLUDED.publisher, arch = EXCLUDED.arch, install_date = EXCLUDED.install_date,
    collection_source = EXCLUDED.collection_source, last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleOSSoftware :exec
DELETE FROM os_software WHERE device_id = $1 AND collection_source = $2 AND last_seen_at < $3;

-- --- os roles (free-form, OS-detected) ---
-- name: ListOSRoles :many
SELECT * FROM os_roles WHERE device_id = $1 ORDER BY role;

-- name: UpsertOSRole :exec
INSERT INTO os_roles (device_id, role, collection_source, last_seen_at)
VALUES ($1,$2,$3,$4)
ON CONFLICT (device_id, role) DO UPDATE SET
    collection_source = EXCLUDED.collection_source, last_seen_at = EXCLUDED.last_seen_at;

-- name: DeleteStaleOSRoles :exec
DELETE FROM os_roles WHERE device_id = $1 AND collection_source = $2 AND last_seen_at < $3;
