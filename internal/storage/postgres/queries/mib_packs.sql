-- name: ListMibPacks :many
SELECT * FROM mib_packs ORDER BY (source='user') DESC, priority ASC, name;

-- name: GetMibPack :one
SELECT * FROM mib_packs WHERE id = $1;

-- name: CreateMibPack :one
INSERT INTO mib_packs (name, vendor, category, source, enabled, priority, version, description, applies_to, parse_meta)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
RETURNING *;

-- name: UpdateMibPack :one
UPDATE mib_packs SET
    name=$2, vendor=$3, category=$4, enabled=$5, priority=$6, version=$7,
    description=$8, applies_to=$9, updated_at=now()
WHERE id=$1 RETURNING *;

-- name: SetMibPackEnabled :exec
UPDATE mib_packs SET enabled=$2, updated_at=now() WHERE id=$1;

-- name: SetMibPackParseMeta :exec
UPDATE mib_packs SET parse_meta=$2, updated_at=now() WHERE id=$1;

-- name: SetMibPackTested :exec
UPDATE mib_packs SET last_tested_at=now(), last_test_detail=$2, last_matched_device=$3, updated_at=now() WHERE id=$1;

-- name: SetMibPackCollected :exec
UPDATE mib_packs SET last_collected_at=now(), last_matched_device=$2, updated_at=now() WHERE id=$1;

-- name: DeleteMibPack :exec
DELETE FROM mib_packs WHERE id=$1;

-- name: CountMibPacksBySource :many
SELECT source, count(*) AS n FROM mib_packs GROUP BY source;

-- ===== Pack files =====

-- name: InsertMibPackFile :exec
INSERT INTO mib_pack_files (pack_id, filename, module_name, content, size_bytes, parse_status, parse_detail)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (pack_id, filename) DO UPDATE SET
    module_name=EXCLUDED.module_name, content=EXCLUDED.content, size_bytes=EXCLUDED.size_bytes,
    parse_status=EXCLUDED.parse_status, parse_detail=EXCLUDED.parse_detail;

-- name: ListMibPackFiles :many
SELECT id, pack_id, filename, module_name, size_bytes, parse_status, parse_detail, created_at
FROM mib_pack_files WHERE pack_id=$1 ORDER BY filename;

-- ===== Pack tables (mappings) =====

-- name: UpsertMibPackTable :one
INSERT INTO mib_pack_tables (pack_id, table_name, root_oid, purpose, column_map, enabled)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (pack_id, table_name) DO UPDATE SET
    root_oid=EXCLUDED.root_oid, purpose=EXCLUDED.purpose, column_map=EXCLUDED.column_map, enabled=EXCLUDED.enabled
RETURNING *;

-- name: ListMibPackTables :many
SELECT * FROM mib_pack_tables WHERE pack_id=$1 ORDER BY purpose, table_name;

-- name: DeleteMibPackTable :exec
DELETE FROM mib_pack_tables WHERE id=$1;

-- ===== Raw walk rows =====

-- name: InsertMibWalkRow :exec
INSERT INTO mib_walk_rows (device_id, pack_id, table_name, oid, idx, raw_value, val_type)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: DeleteMibWalkRows :exec
DELETE FROM mib_walk_rows WHERE device_id=$1 AND table_name=$2;

-- name: DeleteAllMibWalkRows :exec
DELETE FROM mib_walk_rows WHERE device_id=$1;

-- name: ListMibWalkRows :many
SELECT * FROM mib_walk_rows WHERE device_id=$1 ORDER BY table_name, oid LIMIT $2;

-- name: CountMibWalkRows :one
SELECT count(*) FROM mib_walk_rows WHERE device_id=$1;
