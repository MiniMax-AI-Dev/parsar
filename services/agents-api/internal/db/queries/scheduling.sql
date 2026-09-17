-- name: TryExecutionLease :one
SELECT pg_try_advisory_lock(706172736172::bigint)::boolean;

-- name: ListExecutionWork :many
SELECT t.id, t.session_id, s.tenant_id, t.status
FROM turns t JOIN sessions s ON s.id = t.session_id
WHERE t.status = ANY(sqlc.arg(statuses)::text[]) AND t.id > sqlc.arg(after_id)::uuid
AND (s.deleted_at IS NULL OR t.status <> 'queued')
AND (NOT sqlc.arg(connected_only)::boolean OR EXISTS (
    SELECT 1 FROM devices d WHERE d.tenant_id = s.tenant_id AND d.revoked_at IS NULL
        AND d.id = ANY(sqlc.arg(connected_devices)::uuid[])
))
ORDER BY t.id LIMIT 100;

-- name: ListEnvironmentInputWork :many
SELECT r.id, r.session_id, s.tenant_id
FROM environment_input_reservations r
JOIN sessions s ON s.id = r.session_id
LEFT JOIN session_devices b ON b.session_id = s.id
WHERE r.state = 'pending' AND r.deadline > clock_timestamp()
AND r.id > sqlc.arg(after_id)::uuid AND s.deleted_at IS NULL
AND EXISTS (
    SELECT 1 FROM devices d WHERE d.tenant_id = s.tenant_id AND d.revoked_at IS NULL
        AND d.id = ANY(sqlc.arg(connected_devices)::uuid[])
        AND (b.device_id IS NULL OR d.id = b.device_id)
)
ORDER BY r.id LIMIT 100;

-- name: ListExecutionDevices :many
SELECT id, name FROM devices
WHERE tenant_id = $1 AND revoked_at IS NULL AND environment_id IS NULL
ORDER BY id;

-- name: GetLatestSessionTurn :one
SELECT * FROM turns WHERE session_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1;
