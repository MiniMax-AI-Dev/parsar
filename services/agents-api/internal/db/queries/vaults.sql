-- name: CreateVault :one
INSERT INTO vaults (id, tenant_id, name, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetVault :one
SELECT * FROM vaults WHERE tenant_id = $1 AND id = $2;
