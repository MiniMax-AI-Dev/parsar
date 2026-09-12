-- name: CreateAgent :one
INSERT INTO agents (id, tenant_id, metadata, configuration)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAgent :one
SELECT * FROM agents WHERE tenant_id = $1 AND id = $2;
