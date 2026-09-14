-- name: GetEnvironmentConnection :one
SELECT * FROM environment_connections WHERE environment_id = $1;

-- name: ReplaceEnvironmentConnection :exec
INSERT INTO environment_connections (environment_id, generation, revision)
VALUES ($1, $2, 0)
ON CONFLICT (environment_id) DO UPDATE SET generation = EXCLUDED.generation, revision = 0;

-- name: AdvanceEnvironmentConnection :exec
UPDATE environment_connections SET revision = $2 WHERE environment_id = $1;

-- name: SetEnvironmentConnectionStatus :exec
UPDATE environments SET status = $2 WHERE id = $1;

-- name: DeleteEnvironmentConnection :exec
DELETE FROM environment_connections WHERE environment_id = $1;

-- name: ListEnvironmentConnections :many
SELECT e.id, e.session_id, s.tenant_id
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE e.id > $1 AND s.deleted_at IS NULL
  AND (e.status = 'connected' OR EXISTS (
      SELECT 1 FROM environment_connections c WHERE c.environment_id = e.id
  ))
ORDER BY e.id LIMIT 32;
