-- name: CreateAgent :one
INSERT INTO agents (id, tenant_id, metadata, configuration)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAgent :one
SELECT * FROM agents WHERE tenant_id = $1 AND id = $2;

-- name: ListAgents :many
SELECT * FROM agents
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (NOT sqlc.arg(ascending)::boolean AND (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
       OR (sqlc.arg(ascending)::boolean AND (created_at, id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN id END DESC
LIMIT sqlc.arg(page_limit);

-- name: LockAgent :one
SELECT * FROM agents WHERE tenant_id = $1 AND id = $2 FOR UPDATE;

-- name: UpdateAgent :one
UPDATE agents SET configuration = $3, metadata = $4, updated_at = clock_timestamp()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: DeleteAgent :one
DELETE FROM agents WHERE tenant_id = $1 AND id = $2 RETURNING id;
