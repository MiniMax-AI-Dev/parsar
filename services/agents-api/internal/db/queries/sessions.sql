-- name: CreateSession :one
INSERT INTO sessions (id, tenant_id, engine, metadata, idempotency_key, request_hash)
VALUES ($1, $2, $3, $4, $5, $6)
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
       OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);
