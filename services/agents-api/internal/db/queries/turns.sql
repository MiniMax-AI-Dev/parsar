-- name: LockSession :one
SELECT id FROM sessions WHERE tenant_id = $1 AND id = $2 FOR UPDATE;

-- name: GetActiveTurn :one
SELECT * FROM turns WHERE session_id = $1 AND status IN ('queued', 'in_progress', 'waiting');

-- name: CreateTurn :one
INSERT INTO turns(id, session_id) VALUES ($1, $2) RETURNING *;

-- name: GetTurn :one
SELECT t.* FROM turns t JOIN sessions s ON s.id = t.session_id
WHERE s.tenant_id = $1 AND t.session_id = $2 AND t.id = $3;

-- name: FindTurnInput :one
SELECT sequence, turn_id, (kind = sqlc.arg(kind) AND payload = sqlc.arg(payload)::jsonb)::boolean AS matches
FROM turn_inputs WHERE session_id = $1 AND idempotency_key = $2;

-- name: CreateTurnInput :one
INSERT INTO turn_inputs(session_id, turn_id, idempotency_key, kind, payload)
VALUES ($1, $2, $3, $4, $5) RETURNING sequence;

-- name: RequestTurnCancel :exec
UPDATE turns SET cancel_requested_at = COALESCE(cancel_requested_at, clock_timestamp()),
    completed_at = CASE WHEN status = 'queued' THEN clock_timestamp() ELSE completed_at END,
    status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END
WHERE id = $1 AND session_id = $2 AND status IN ('queued', 'in_progress', 'waiting');

-- name: TransitionTurn :one
UPDATE turns SET status = sqlc.arg(new_status), outcome = sqlc.arg(outcome),
    started_at = CASE WHEN sqlc.arg(new_status)::text = 'in_progress'
        THEN COALESCE(started_at, clock_timestamp()) ELSE started_at END,
    completed_at = CASE WHEN sqlc.arg(new_status)::text IN ('completed', 'failed', 'cancelled')
        THEN clock_timestamp() ELSE NULL END
WHERE id = sqlc.arg(id) AND session_id = sqlc.arg(session_id)
    AND status = sqlc.arg(expected_status)
    AND status IN ('queued', 'in_progress', 'waiting')
    AND (sqlc.arg(new_status)::text <> 'in_progress' OR cancel_requested_at IS NULL)
RETURNING *;

-- name: ListTurnInputs :many
SELECT i.* FROM turn_inputs i JOIN sessions s ON s.id = i.session_id
WHERE s.tenant_id = $1 AND i.session_id = $2 AND i.turn_id = $3 AND i.sequence > $4
ORDER BY i.sequence LIMIT $5;
