-- name: CreateSubnet :one
INSERT INTO subnets (location_id, cidr, name, vlan_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListSubnetsByLocation :many
SELECT * FROM subnets WHERE location_id = $1 ORDER BY cidr;

-- name: ListSubnets :many
SELECT * FROM subnets ORDER BY location_id, cidr;

-- name: DeleteSubnet :exec
DELETE FROM subnets WHERE id = $1;
