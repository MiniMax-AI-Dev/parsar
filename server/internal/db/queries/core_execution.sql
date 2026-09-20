-- name: EnsureProductCoreSession :one
INSERT INTO product_core_sessions (id, workspace_id, conversation_id, agent_id, request)
SELECT @id::uuid, r.workspace_id, r.conversation_id, r.agent_id, @request::jsonb
FROM agent_runs r WHERE r.id = @run_id::uuid
ON CONFLICT (conversation_id, agent_id) DO UPDATE SET id = product_core_sessions.id
RETURNING *;

-- name: BindProductCoreSession :one
UPDATE product_core_sessions SET core_session_id = @core_session_id
WHERE id = @id::uuid AND (core_session_id = '' OR core_session_id = @core_session_id)
RETURNING *;

-- name: EnsureProductCoreRun :one
INSERT INTO product_core_runs (run_id, session_binding_id, input)
VALUES (@run_id::uuid, @session_binding_id::uuid, @input::jsonb)
ON CONFLICT (run_id) DO UPDATE SET run_id = product_core_runs.run_id
RETURNING *;

-- name: SetProductCoreRunBaseline :one
UPDATE product_core_runs SET previous_turn_id = COALESCE(previous_turn_id, @previous_turn_id::text)
WHERE run_id = @run_id::uuid RETURNING *;

-- name: MarkProductCoreRunSubmitted :exec
UPDATE product_core_runs SET submitted = true WHERE run_id = @run_id::uuid;

-- name: MarkProductCoreRunAttempted :exec
UPDATE product_core_runs SET attempted = true WHERE run_id = @run_id::uuid;

-- name: BindProductCoreTurn :one
UPDATE product_core_runs SET core_turn_id = @core_turn_id
WHERE run_id = @run_id::uuid AND (core_turn_id = '' OR core_turn_id = @core_turn_id)
RETURNING *;

-- name: GetProductCoreRunBinding :one
SELECT r.*, s.core_session_id, s.workspace_id, s.conversation_id, s.agent_id
FROM product_core_runs r JOIN product_core_sessions s ON s.id = r.session_binding_id
WHERE r.run_id = @run_id::uuid;

-- name: ListRecoverableProductCoreRuns :many
SELECT r.id::text AS id, r.conversation_id::text AS conversation_id, r.status
FROM agent_runs r
LEFT JOIN product_core_runs cr ON cr.run_id = r.id
WHERE r.connector_type = 'agents_api' AND
  (r.status IN ('queued', 'running') OR (r.status IN ('cancelled', 'failed', 'completed') AND cr.settled = false))
ORDER BY r.created_at, r.id;

-- name: SettleProductCoreRun :exec
UPDATE product_core_runs SET settled = true WHERE run_id = @run_id::uuid;

-- name: LockProductCoreRun :one
SELECT pg_try_advisory_xact_lock(hashtextextended(r.conversation_id::text || ':' || r.agent_id::text, 71825))::boolean
FROM agent_runs r WHERE r.id = @run_id::uuid;

-- name: ProductCoreRunHasPredecessor :one
SELECT EXISTS (
  SELECT 1 FROM agent_runs older
  LEFT JOIN product_core_runs cr ON cr.run_id = older.id
  JOIN agent_runs target ON target.id = @run_id::uuid
  WHERE older.conversation_id = target.conversation_id AND older.agent_id = target.agent_id
    AND (older.created_at, older.id) < (target.created_at, target.id)
    AND (older.status IN ('queued', 'running') OR (older.status IN ('cancelled', 'failed', 'completed') AND cr.settled = false))
)::boolean;

-- name: GetProductCoreExecutionStatus :one
-- Internal execution cleanup remains reachable after product history is hidden.
SELECT status FROM agent_runs WHERE id = @id::uuid AND connector_type = 'agents_api';
