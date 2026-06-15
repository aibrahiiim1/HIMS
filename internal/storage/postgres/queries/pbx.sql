-- name: UpsertPbxPhone :exec
INSERT INTO pbx_phones (device_id, name, model, description, device_pool, collection_source, last_seen_at, extension, mac_address, ip_address, registration, registrar)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (device_id, name) DO UPDATE SET
    model = EXCLUDED.model,
    description = EXCLUDED.description,
    device_pool = EXCLUDED.device_pool,
    collection_source = EXCLUDED.collection_source,
    last_seen_at = EXCLUDED.last_seen_at,
    extension = COALESCE(NULLIF(EXCLUDED.extension,''), pbx_phones.extension),
    mac_address = COALESCE(NULLIF(EXCLUDED.mac_address,''), pbx_phones.mac_address),
    ip_address = COALESCE(NULLIF(EXCLUDED.ip_address,''), pbx_phones.ip_address),
    registration = COALESCE(NULLIF(EXCLUDED.registration,''), pbx_phones.registration),
    registrar = COALESCE(NULLIF(EXCLUDED.registrar,''), pbx_phones.registrar);

-- name: FindPhoneByIP :many
-- Path Finder: which phone(s) carry this IP, the directory number, registration
-- status + the CM node (registrar), and the owning PBX device (CUCM cluster).
SELECT p.extension, p.name, p.model, p.description, p.registration, p.registrar,
       p.device_id AS pbx_device_id, d.name AS pbx_name, d.category AS pbx_category
FROM pbx_phones p JOIN devices d ON d.id = p.device_id AND d.deleted_at IS NULL
WHERE p.ip_address = $1
ORDER BY p.extension
LIMIT 10;

-- name: FindPhoneByMAC :many
-- Path Finder: resolve a MAC to the IP phone that carries it (CUCM SEP<mac>),
-- with directory number, registration + registrar. Compares the MAC ignoring
-- separators/case so 00:23:eb:.. , 0023eb.. and 00-23-.. all match.
SELECT p.extension, p.name, p.model, p.description, p.registration, p.registrar,
       p.device_id AS pbx_device_id, d.name AS pbx_name, d.category AS pbx_category
FROM pbx_phones p JOIN devices d ON d.id = p.device_id AND d.deleted_at IS NULL
WHERE lower(translate(p.mac_address, ':-.', '')) = lower(translate($1, ':-.', ''))
ORDER BY (p.registration IS NOT NULL) DESC, p.extension
LIMIT 10;

-- name: SearchPhones :many
-- Global search: IP phones / PBX subscribers by extension / SEP name / MAC / IP.
-- Deduped across the CUCM pub/sub pair (the same phone appears under each node).
SELECT DISTINCT ON (p.name) p.extension, p.name, p.model, p.registration, p.registrar,
       p.ip_address, p.mac_address, p.device_id AS pbx_device_id, d.name AS pbx_name, d.category AS pbx_category
FROM pbx_phones p
JOIN devices d ON d.id = p.device_id AND d.deleted_at IS NULL
WHERE p.extension ILIKE '%'||$1||'%' OR p.name ILIKE '%'||$1||'%'
   OR p.mac_address ILIKE '%'||$1||'%' OR p.ip_address ILIKE '%'||$1||'%'
ORDER BY p.name, (p.registration IS NOT NULL) DESC
LIMIT 40;

-- name: ListPbxPhones :many
SELECT * FROM pbx_phones WHERE device_id = $1 ORDER BY name;

-- name: DeleteStalePbxPhones :exec
DELETE FROM pbx_phones
WHERE device_id = $1 AND last_seen_at < $2 AND collection_source = $3;
