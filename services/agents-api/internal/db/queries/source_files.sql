-- name: CreateSourceFile :one
INSERT INTO source_files (id, tenant_id, filename, purpose, body_oid, size_bytes, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSourceFile :one
SELECT * FROM source_files WHERE tenant_id = $1 AND id = $2;

-- name: DeleteSourceFile :one
DELETE FROM source_files WHERE tenant_id = $1 AND id = $2 RETURNING body_oid;
