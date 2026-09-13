-- name: ExpireDueEnvironmentInputs :execrows
WITH candidates AS MATERIALIZED (
    SELECT r.id, r.session_id
    FROM environment_input_reservations r
    JOIN sessions s ON s.id = r.session_id
    WHERE r.state = 'pending'
      AND r.deadline <= statement_timestamp()
      AND s.deleted_at IS NULL
    ORDER BY r.deadline, r.id
    LIMIT 32
    FOR UPDATE OF s SKIP LOCKED
)
UPDATE environment_input_reservations r
SET state = 'expired', settled_at = clock_timestamp()
FROM candidates c
WHERE r.id = c.id AND r.session_id = c.session_id
  AND r.state = 'pending'
  AND r.deadline <= clock_timestamp();
