-- name: GetSessionItem :one
SELECT * FROM session_items WHERE session_id = $1 AND id = $2;

-- name: PutSessionItem :one
INSERT INTO session_items(id, session_id, turn_id, created_at, payload, position, output_index)
VALUES (sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(turn_id), sqlc.arg(created_at), sqlc.arg(payload),
    (SELECT COALESCE(max(position), -1) + 1 FROM session_items WHERE session_id = sqlc.arg(session_id)),
    CASE WHEN sqlc.arg(is_output)::boolean THEN
        (SELECT COALESCE(max(output_index), -1) + 1 FROM session_items WHERE turn_id = sqlc.arg(turn_id))
    END)
ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload
RETURNING *;

-- name: ListSessionItems :many
SELECT i.id, i.created_at,
    (CASE WHEN i.payload->>'status' = 'in_progress' AND t.status IN ('completed', 'failed', 'cancelled')
         THEN jsonb_set(i.payload, '{status}', '"incomplete"') ELSE i.payload END)::jsonb AS payload
FROM session_items i JOIN turns t ON t.id = i.turn_id
WHERE i.session_id = sqlc.arg(session_id)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (sqlc.arg(ascending)::boolean AND (i.created_at, i.position, i.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_position)::integer, sqlc.arg(after_id)::uuid))
       OR (NOT sqlc.arg(ascending)::boolean AND (i.created_at, i.position, i.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_position)::integer, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.position END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.position END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.id END DESC
LIMIT sqlc.arg(page_limit);

-- name: UnindexedItemTurn :one
SELECT * FROM turns WHERE session_id = $1 AND NOT items_indexed ORDER BY created_at, id LIMIT 1;

-- name: MarkItemsIndexed :exec
UPDATE turns SET items_indexed = true WHERE session_id = $1 AND id = $2;

-- name: ItemSources :many
SELECT kind, payload, created_at, source_order, source_type FROM (
    SELECT kind, payload, created_at, sequence AS source_order, 0::integer AS source_type
    FROM turn_inputs inp WHERE inp.session_id = sqlc.arg(session_id) AND inp.turn_id = sqlc.arg(turn_id) AND kind = 'message'
    UNION ALL
    SELECT kind, payload, created_at, ordinal::bigint AS source_order, 1::integer AS source_type
    FROM turn_events evt WHERE evt.session_id = sqlc.arg(session_id) AND evt.turn_id = sqlc.arg(turn_id)
) AS sources
WHERE (sqlc.narg(after_created)::timestamptz IS NULL
    OR (created_at, source_type, source_order) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_type)::integer, sqlc.arg(after_order)::bigint))
ORDER BY created_at, source_type, source_order LIMIT 100;

-- name: ItemInputSource :one
SELECT * FROM turn_inputs WHERE session_id = $1 AND sequence = $2;

-- name: ItemEventSources :many
SELECT * FROM turn_events WHERE session_id = $1 AND turn_id = $2 AND ordinal >= $3 ORDER BY ordinal;

-- name: HasNativeMessageItem :one
SELECT EXISTS(SELECT 1 FROM session_items WHERE turn_id = $1
    AND payload->>'role' = 'assistant' AND id <> $2);
