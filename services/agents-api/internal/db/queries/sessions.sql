-- name: CreateSession :one
INSERT INTO sessions (id, tenant_id, engine, metadata, idempotency_key, request_hash, configuration, creation_request_hash, creator_kind, creator_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (tenant_id, idempotency_key) DO UPDATE
SET idempotency_key = EXCLUDED.idempotency_key
WHERE sessions.deleted_at IS NULL
  AND sessions.creator_kind = EXCLUDED.creator_kind AND sessions.creator_id = EXCLUDED.creator_id
  AND CASE WHEN sessions.creation_request_hash IS NULL
    THEN sessions.request_hash = EXCLUDED.request_hash
    ELSE sessions.creation_request_hash = EXCLUDED.creation_request_hash END
RETURNING *;

-- name: GetSession :one
SELECT * FROM sessions WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: ListSessions :many
SELECT * FROM sessions
WHERE tenant_id = sqlc.arg(tenant_id) AND deleted_at IS NULL
  AND (sqlc.narg(agent_id)::text IS NULL OR configuration #>> '{agent,id}' = sqlc.narg(agent_id)::text)
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
UPDATE sessions SET metadata = $3 WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL RETURNING *;

-- name: FindSessionCreation :one
SELECT * FROM sessions
WHERE tenant_id = $1 AND idempotency_key = $2;

-- name: MarkSessionDeleted :exec
UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1 AND deleted_at IS NULL;
