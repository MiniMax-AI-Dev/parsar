-- name: CreateEnvironmentInputReservation :one
WITH accepted AS (SELECT clock_timestamp() AS at)
INSERT INTO environment_input_reservations(id, session_id, idempotency_key, batch, is_initial, created_at, deadline)
SELECT $1, $2, $3, $4, $5, at, at + interval '5 minutes' FROM accepted
RETURNING *;

-- name: FindEnvironmentInputReservation :one
SELECT sqlc.embed(r), r.batch = sqlc.arg(batch)::jsonb AS matches
FROM environment_input_reservations r
WHERE r.session_id = sqlc.arg(session_id) AND r.idempotency_key = sqlc.arg(idempotency_key);

-- name: GetEnvironmentInputReservation :one
SELECT * FROM environment_input_reservations
WHERE session_id = $1 AND id = $2;

-- name: CheckEnvironmentInputGate :one
SELECT COALESCE((
    SELECT r.batch = sqlc.arg(batch)::jsonb FROM environment_input_reservations r
    WHERE r.session_id = sqlc.arg(session_id) AND r.idempotency_key = sqlc.arg(idempotency_key)
), true)::boolean AS matches,
EXISTS (
    SELECT 1 FROM environment_input_reservations r
    WHERE r.session_id = sqlc.arg(session_id)
      AND (r.state = 'pending' OR r.idempotency_key = sqlc.arg(idempotency_key))
)::boolean AS blocked;

-- name: ExpireEnvironmentInputReservation :exec
UPDATE environment_input_reservations SET state = 'expired', settled_at = clock_timestamp()
WHERE session_id = $1 AND id = $2 AND state = 'pending' AND deadline <= clock_timestamp();

-- name: SettleEnvironmentInputReservation :one
UPDATE environment_input_reservations SET state = sqlc.arg(state), settled_at = clock_timestamp()
WHERE session_id = sqlc.arg(session_id) AND id = sqlc.arg(id) AND state = 'pending'
RETURNING *;

-- name: CancelSessionEnvironmentInput :exec
UPDATE environment_input_reservations SET state = 'cancelled', settled_at = clock_timestamp()
WHERE session_id = $1 AND state = 'pending';
