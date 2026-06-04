-- ===== RBAC: users / roles / permissions ==================================

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: CreateUser :one
INSERT INTO users (username, full_name, email, is_active)
VALUES ($1,$2,$3,$4) RETURNING *;

-- name: UpdateUser :one
UPDATE users SET full_name=$2, email=$3, is_active=$4, updated_at=now()
WHERE id=$1 RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id=$1;

-- name: ListRoles :many
SELECT * FROM roles ORDER BY name;

-- name: CreateRole :one
INSERT INTO roles (name, description) VALUES ($1,$2) RETURNING *;

-- name: DeleteRole :exec
DELETE FROM roles WHERE id=$1;

-- name: ListPermissions :many
SELECT * FROM permissions ORDER BY code;

-- name: CreatePermission :one
INSERT INTO permissions (code, description) VALUES ($1,$2) RETURNING *;

-- name: DeletePermission :exec
DELETE FROM permissions WHERE id=$1;

-- name: RolesForUser :many
SELECT r.* FROM roles r
JOIN user_roles ur ON ur.role_id = r.id
WHERE ur.user_id = $1 ORDER BY r.name;

-- name: SetUserRolesClear :exec
DELETE FROM user_roles WHERE user_id=$1;

-- name: AddUserRole :exec
INSERT INTO user_roles (user_id, role_id) VALUES ($1,$2)
ON CONFLICT DO NOTHING;

-- name: PermissionsForRole :many
SELECT p.* FROM permissions p
JOIN role_permissions rp ON rp.permission_id = p.id
WHERE rp.role_id = $1 ORDER BY p.code;

-- name: SetRolePermissionsClear :exec
DELETE FROM role_permissions WHERE role_id=$1;

-- name: AddRolePermission :exec
INSERT INTO role_permissions (role_id, permission_id) VALUES ($1,$2)
ON CONFLICT DO NOTHING;

-- ===== Device templates ====================================================

-- name: ListDeviceTemplates :many
SELECT * FROM device_templates ORDER BY name;

-- name: CreateDeviceTemplate :one
INSERT INTO device_templates (name, vendor, device_type, discovery_rules, monitoring_rules, classification_rules, enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: UpdateDeviceTemplate :one
UPDATE device_templates
SET name=$2, vendor=$3, device_type=$4, discovery_rules=$5, monitoring_rules=$6,
    classification_rules=$7, enabled=$8, updated_at=now()
WHERE id=$1 RETURNING *;

-- name: DeleteDeviceTemplate :exec
DELETE FROM device_templates WHERE id=$1;

-- ===== Vendor fingerprints =================================================

-- name: ListVendorFingerprints :many
SELECT * FROM vendor_fingerprints ORDER BY kind, vendor, pattern;

-- name: CreateVendorFingerprint :one
INSERT INTO vendor_fingerprints (kind, pattern, vendor, device_type, confidence, enabled)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: UpdateVendorFingerprint :one
UPDATE vendor_fingerprints
SET kind=$2, pattern=$3, vendor=$4, device_type=$5, confidence=$6, enabled=$7
WHERE id=$1 RETURNING *;

-- name: DeleteVendorFingerprint :exec
DELETE FROM vendor_fingerprints WHERE id=$1;

-- ===== Audit log ===========================================================

-- name: InsertAuditLog :exec
INSERT INTO audit_log (actor, action, category, entity_type, entity_id, summary, details)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: ListAuditLog :many
SELECT * FROM audit_log
WHERE (sqlc.narg('category')::text IS NULL OR category = sqlc.narg('category'))
ORDER BY at DESC
LIMIT $1;
