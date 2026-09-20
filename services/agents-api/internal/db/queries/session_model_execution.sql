-- name: SaveSessionModelExecution :exec
INSERT INTO session_model_execution(session_id, encrypted_config) VALUES (@session_id, @encrypted_config);

-- name: GetSessionModelExecution :one
SELECT e.encrypted_config FROM session_model_execution e
JOIN sessions s ON s.id = e.session_id WHERE s.tenant_id = @tenant_id AND s.id = @session_id;
