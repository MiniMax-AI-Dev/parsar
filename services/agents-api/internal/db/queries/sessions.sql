-- name: CreateSession :one
INSERT INTO sessions (id, tenant_id, engine, metadata, idempotency_key, request_hash, configuration)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, idempotency_key) DO UPDATE
SET idempotency_key = EXCLUDED.idempotency_key
WHERE sessions.request_hash = EXCLUDED.request_hash
RETURNING *;

-- name: GetSession :one
SELECT * FROM sessions WHERE tenant_id = $1 AND id = $2;

-- name: ListSessions :many
SELECT * FROM sessions
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

-- name: UpdateSessionMetadata :one
UPDATE sessions SET metadata = $3 WHERE tenant_id = $1 AND id = $2 RETURNING *;
