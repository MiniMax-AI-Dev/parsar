-- name: PutTurnUsage :exec
UPDATE turns SET token_usage = $3 WHERE session_id = $1 AND id = $2;

-- name: SessionTokenUsage :one
SELECT CASE WHEN count(token_usage) = 0 THEN NULL ELSE jsonb_build_object(
 'input_tokens', sum((token_usage->>'input_tokens')::numeric),
 'input_tokens_details', jsonb_build_object('cached_tokens', sum((token_usage->'input_tokens_details'->>'cached_tokens')::numeric)),
 'output_tokens', sum((token_usage->>'output_tokens')::numeric),
 'output_tokens_details', jsonb_build_object('reasoning_tokens', sum((token_usage->'output_tokens_details'->>'reasoning_tokens')::numeric)),
 'total_tokens', sum((token_usage->>'total_tokens')::numeric)
) END::jsonb AS usage
FROM turns WHERE session_id = $1;
