-- name: GetEnvironmentFileWrite :one
SELECT sqlc.embed(w), e.session_id
FROM environment_file_writes w
JOIN environments e ON e.id = w.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = sqlc.arg(tenant_id) AND e.id = sqlc.arg(environment_id) AND w.id = sqlc.arg(id);

-- name: CreateEnvironmentFileWrite :one
INSERT INTO environment_file_writes(id, environment_id, device_id, request_sha256)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: SettleEnvironmentFileWrite :one
UPDATE environment_file_writes SET state = $3, settled_at = clock_timestamp()
WHERE environment_id = $1 AND id = $2 AND state = 'pending' RETURNING *;

-- name: EnvironmentFileWriteBlocksSession :one
SELECT EXISTS (
    SELECT 1 FROM environments e JOIN environment_file_writes w ON w.environment_id = e.id
    WHERE e.session_id = $1 AND w.state = 'pending'
)::boolean;

-- name: EnvironmentFileWriteHasPendingInput :one
SELECT EXISTS (
    SELECT 1 FROM environment_input_reservations WHERE session_id = $1 AND state = 'pending'
)::boolean;
