-- name: CreateEnvironment :exec
INSERT INTO environments (id, session_id) VALUES ($1, $2);

-- name: GetEnvironment :one
SELECT sqlc.embed(e), s.tenant_id, (s.configuration->'environment')::jsonb AS configuration
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND e.id = $2 AND s.deleted_at IS NULL;

-- name: GetSessionEnvironment :one
SELECT sqlc.embed(e), s.tenant_id, (s.configuration->'environment')::jsonb AS configuration
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND s.id = $2 AND s.deleted_at IS NULL;
