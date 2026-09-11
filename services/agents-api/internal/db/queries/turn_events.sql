-- name: InsertTurnEvent :exec
INSERT INTO turn_events(session_id, turn_id, ordinal, kind, payload)
VALUES ($1, $2, $3, $4, $5);

-- name: CountTurnEvent :exec
UPDATE turns SET event_count = event_count + sqlc.arg(event_count)::integer, event_bytes = event_bytes + sqlc.arg(payload_bytes)::bigint
WHERE session_id = $1 AND id = $2;

-- name: MatchTurnEventBatch :one
SELECT COALESCE(jsonb_agg(jsonb_build_object('kind', e.kind, 'payload', e.payload) ORDER BY ordinal)
    = sqlc.arg(batch)::jsonb, false)::boolean AS matches
FROM turn_events e WHERE session_id = $1 AND turn_id = $2
    AND ordinal >= sqlc.arg(first_ordinal) AND ordinal < sqlc.arg(first_ordinal)::integer + sqlc.arg(event_count)::integer;

-- name: InsertTurnEventBatch :exec
INSERT INTO turn_events(session_id, turn_id, ordinal, kind, payload)
SELECT $1, $2, sqlc.arg(first_ordinal)::integer + item.n::integer - 1, item.value->>'kind', item.value->'payload'
FROM jsonb_array_elements(sqlc.arg(batch)::jsonb) WITH ORDINALITY AS item(value, n);

-- name: ListTurnEvents :many
SELECT e.* FROM turn_events e JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND e.session_id = $2 AND e.turn_id = $3 AND e.ordinal > $4
ORDER BY e.ordinal LIMIT $5;
