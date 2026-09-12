-- name: MatchFunctionCall :one
SELECT COALESCE(executor_call_id = sqlc.arg(executor_call_id) AND name = sqlc.arg(name)
    AND arguments = sqlc.arg(arguments)::jsonb, false)::boolean AS matches
FROM function_calls
WHERE session_id = sqlc.arg(session_id) AND turn_id = sqlc.arg(turn_id) AND call_id = sqlc.arg(call_id);

-- name: CreateFunctionCall :execrows
INSERT INTO function_calls(session_id, turn_id, call_id, executor_call_id, name, arguments)
VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING;

-- name: GetFunctionCall :one
SELECT f.* FROM function_calls f JOIN sessions s ON s.id = f.session_id
WHERE s.tenant_id = $1 AND f.session_id = $2 AND f.turn_id = $3 AND f.call_id = $4;

-- name: ListPendingFunctionCalls :many
SELECT f.* FROM function_calls f JOIN turns t ON t.session_id = f.session_id AND t.id = f.turn_id
WHERE f.session_id = $1 AND f.turn_id = $2 AND NOT f.applied
    AND t.status IN ('in_progress', 'waiting') AND t.cancel_requested_at IS NULL
ORDER BY f.created_at, f.call_id;

-- name: MatchFunctionResult :one
SELECT (result IS NOT NULL)::boolean AS submitted, COALESCE(result = sqlc.arg(result)::jsonb, false)::boolean AS matches
FROM function_calls
WHERE session_id = sqlc.arg(session_id) AND turn_id = sqlc.arg(turn_id) AND call_id = sqlc.arg(call_id);

-- name: SubmitFunctionResult :exec
UPDATE function_calls SET result = $4 WHERE session_id = $1 AND turn_id = $2 AND call_id = $3;

-- name: ApplyFunctionResult :exec
UPDATE function_calls SET applied = true WHERE session_id = $1 AND turn_id = $2 AND call_id = $3;
