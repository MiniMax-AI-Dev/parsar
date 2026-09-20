-- name: CreateEnvironmentSetup :exec
INSERT INTO environment_setups (session_id, contents) VALUES ($1, $2);

-- name: GetEnvironmentSetup :one
SELECT f.contents FROM sessions s LEFT JOIN environment_setups f ON s.id = f.session_id
WHERE s.tenant_id = $1 AND s.id = $2 AND s.deleted_at IS NULL;

-- name: SetSessionSetupMetadata :one
UPDATE sessions SET configuration = jsonb_set(jsonb_set(configuration, '{environment,packages}', $2::jsonb), '{environment,initialization}', 'true'::jsonb)
WHERE id = $1 RETURNING *;
