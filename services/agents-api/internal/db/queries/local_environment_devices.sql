-- name: CreateEnvironmentDevice :one
INSERT INTO devices (id, tenant_id, name, credential_hash, environment_id)
SELECT sqlc.arg(id), s.tenant_id, sqlc.arg(name), sqlc.arg(credential_hash), e.id
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = sqlc.arg(tenant_id) AND e.id = sqlc.arg(environment_id)
AND s.deleted_at IS NULL AND s.configuration->'environment'->>'type' = 'openai_hosted'
ON CONFLICT (environment_id) DO NOTHING
RETURNING id;
