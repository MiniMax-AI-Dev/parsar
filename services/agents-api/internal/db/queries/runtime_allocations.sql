-- name: CreateRuntimeAllocation :one
INSERT INTO runtime_allocations (id, environment_id, device_id, provider_key)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetRuntimeAllocation :one
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at, (a.kept_at <= clock_timestamp() - interval '1 hour') AS expired
FROM runtime_allocations a
JOIN environments e ON e.id = a.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND a.environment_id = $2;

-- name: ListRuntimeAllocations :many
SELECT sqlc.embed(a), e.session_id, s.tenant_id, s.deleted_at, (a.kept_at <= clock_timestamp() - interval '1 hour') AS expired
FROM runtime_allocations a
JOIN environments e ON e.id = a.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE a.id > $1 AND a.state <> 'released'
ORDER BY a.id LIMIT 32;

-- name: ObserveRuntimeRunning :one
UPDATE runtime_allocations SET state = 'running', create_settled = true
WHERE id = $1 AND state IN ('creating', 'running')
AND kept_at > clock_timestamp() - interval '1 hour'
RETURNING *;

-- name: KeepRuntimeAllocation :one
UPDATE runtime_allocations SET kept_at = clock_timestamp()
WHERE id = $1 AND state = 'running'
AND kept_at > clock_timestamp() - interval '1 hour'
RETURNING *;

-- name: RequestRuntimeCleanup :one
UPDATE runtime_allocations SET state = 'cleanup_pending'
WHERE id = $1 AND state <> 'released' RETURNING *;

-- name: SettleRuntimeCreation :one
UPDATE runtime_allocations SET create_settled = true
WHERE id = $1 AND state <> 'released' RETURNING *;

-- name: ReleaseRuntimeAllocation :one
UPDATE runtime_allocations SET state = 'released', released_at = clock_timestamp()
WHERE id = $1 AND state = 'cleanup_pending' AND create_settled RETURNING *;

-- name: ListUnallocatedHostedEnvironments :many
SELECT e.id, s.tenant_id
FROM environments e JOIN sessions s ON s.id = e.session_id
WHERE e.id > $1 AND s.deleted_at IS NULL AND e.status = 'pending'
  AND s.configuration->'environment'->>'type' = 'openai_hosted'
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id)
ORDER BY e.id LIMIT 32;
