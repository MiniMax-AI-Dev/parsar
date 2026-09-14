-- name: GetEnvironmentInputActivity :one
SELECT r.state, r.created_at, r.settled_at, e.id AS environment_id, e.status AS connection_status
FROM environments e
JOIN LATERAL (
    SELECT * FROM environment_input_reservations
    WHERE session_id = e.session_id
    ORDER BY created_at DESC, id DESC LIMIT 1
) r ON true
WHERE e.session_id = $1 AND r.state <> 'admitted'
  AND NOT EXISTS (
      SELECT 1 FROM turns t WHERE t.session_id = e.session_id
        AND (t.created_at >= r.created_at OR t.status IN ('queued', 'in_progress', 'waiting'))
  );
