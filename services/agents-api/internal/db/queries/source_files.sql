-- name: CreateSourceFile :one
INSERT INTO source_files (id, tenant_id, filename, purpose, body_oid, size_bytes, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSourceFile :one
SELECT * FROM source_files WHERE tenant_id = $1 AND id = $2;

-- name: ListSourceFiles :many
SELECT * FROM source_files
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(purpose)::text IS NULL OR purpose = sqlc.narg(purpose)::text)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
    OR (NOT sqlc.arg(ascending)::boolean AND (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
    OR (sqlc.arg(ascending)::boolean AND (created_at, id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
  CASE WHEN sqlc.arg(ascending)::boolean THEN created_at END ASC,
  CASE WHEN sqlc.arg(ascending)::boolean THEN id END ASC,
  CASE WHEN NOT sqlc.arg(ascending)::boolean THEN created_at END DESC,
  CASE WHEN NOT sqlc.arg(ascending)::boolean THEN id END DESC
LIMIT sqlc.arg(page_limit);

-- name: DeleteSourceFile :one
DELETE FROM source_files WHERE tenant_id = $1 AND id = $2 RETURNING body_oid;
