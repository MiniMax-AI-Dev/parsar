-- name: ListTurns :many
SELECT t.* FROM turns t JOIN sessions s ON s.id = t.session_id
WHERE s.tenant_id = sqlc.arg(tenant_id) AND t.session_id = sqlc.arg(session_id)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (NOT sqlc.arg(ascending)::boolean AND (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
       OR (sqlc.arg(ascending)::boolean AND (t.created_at, t.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN t.created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN t.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN t.created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN t.id END DESC
LIMIT sqlc.arg(page_limit);
