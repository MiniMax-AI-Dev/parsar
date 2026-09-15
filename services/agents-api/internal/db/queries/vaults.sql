-- name: CreateVault :one
INSERT INTO vaults (id, tenant_id, name, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetVault :one
SELECT * FROM vaults WHERE tenant_id = $1 AND id = $2;

-- name: DeleteVault :one
DELETE FROM vaults WHERE tenant_id = $1 AND id = $2 RETURNING id;

-- name: ListVaults :many
SELECT * FROM vaults
WHERE tenant_id = sqlc.arg(tenant_id)
  AND status = ANY(sqlc.arg(statuses)::text[])
  AND (sqlc.narg(after_created)::timestamptz IS NULL
    OR (NOT sqlc.arg(ascending)::boolean AND (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
    OR (sqlc.arg(ascending)::boolean AND (created_at, id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
  CASE WHEN sqlc.arg(ascending)::boolean THEN created_at END ASC,
  CASE WHEN sqlc.arg(ascending)::boolean THEN id END ASC,
  CASE WHEN NOT sqlc.arg(ascending)::boolean THEN created_at END DESC,
  CASE WHEN NOT sqlc.arg(ascending)::boolean THEN id END DESC
LIMIT sqlc.arg(page_limit);
