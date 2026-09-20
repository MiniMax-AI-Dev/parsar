-- name: CreateInitialEnvironmentFile :exec
INSERT INTO initial_environment_files (id, session_id, position, path, size_bytes, contents)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: SetSessionInitialFileMetadata :one
UPDATE sessions SET configuration = jsonb_set(configuration, '{environment,files}', $2::jsonb)
WHERE id = $1 RETURNING *;

-- name: GetInitialEnvironmentFile :one
SELECT f.* FROM initial_environment_files f JOIN sessions s ON s.id = f.session_id
WHERE s.tenant_id = $1 AND f.session_id = $2 AND f.position = $3 AND s.deleted_at IS NULL;

-- name: GetSessionInitializationReady :one
SELECT NOT EXISTS (
 SELECT 1 FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
 WHERE e.session_id = s.id AND a.initialization <> 'complete'
) AND ((NOT EXISTS (SELECT 1 FROM initial_environment_files f WHERE f.session_id = s.id)
 AND NOT EXISTS (SELECT 1 FROM environment_setups f WHERE f.session_id = s.id))
 OR EXISTS (SELECT 1 FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
 WHERE e.session_id = s.id AND a.initialization = 'complete')) AS ready
FROM sessions s WHERE s.tenant_id = $1 AND s.id = $2;

-- name: ClaimRuntimeInitialization :one
UPDATE runtime_allocations SET initialization = 'running'
WHERE id = $1 AND initialization = 'pending' AND state = 'running' AND create_settled
AND kept_at > clock_timestamp() - interval '1 hour'
RETURNING *;

-- name: CompleteRuntimeInitialization :one
UPDATE runtime_allocations SET initialization = 'complete'
WHERE id = $1 AND initialization = 'running' AND state = 'running' AND create_settled
AND kept_at > clock_timestamp() - interval '1 hour'
RETURNING *;

-- name: LockInitialSourceFile :one
SELECT * FROM source_files WHERE tenant_id = $1 AND id = $2 FOR SHARE;
