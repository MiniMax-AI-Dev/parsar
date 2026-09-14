-- name: ListDueEnvironmentInputs :many
SELECT r.id, r.session_id
FROM environment_input_reservations r
JOIN sessions s ON s.id = r.session_id
WHERE r.state = 'pending'
  AND r.deadline <= statement_timestamp()
  AND s.deleted_at IS NULL
ORDER BY r.deadline, r.id
LIMIT 32
FOR UPDATE OF s SKIP LOCKED;
