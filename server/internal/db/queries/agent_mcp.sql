-- name: PutAgentMCPToken :exec
INSERT INTO agent_mcp_tokens (agent_id, user_id, token_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id, user_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash, created_at = EXCLUDED.created_at,
    expires_at = EXCLUDED.expires_at;

-- name: GetAgentMCPToken :one
SELECT created_at, expires_at FROM agent_mcp_tokens
WHERE agent_id = $1 AND user_id = $2;

-- name: DeleteAgentMCPToken :exec
DELETE FROM agent_mcp_tokens WHERE agent_id = $1 AND user_id = $2;

-- name: ResolveAgentMCPToken :one
SELECT t.agent_id::text, t.user_id::text, a.workspace_id::text, a.name, a.description
FROM agent_mcp_tokens t
JOIN agents a ON a.id = t.agent_id AND a.deleted_at IS NULL AND a.status = 'active'
JOIN workspaces w ON w.id = a.workspace_id AND w.deleted_at IS NULL
JOIN users u ON u.id = t.user_id AND u.deleted_at IS NULL AND u.status = 'active'
JOIN workspace_members m ON m.workspace_id = a.workspace_id AND m.user_id = t.user_id
  AND m.deleted_at IS NULL AND m.status = 'active' AND m.role IN ('owner', 'admin', 'member')
WHERE t.token_hash = $1 AND t.expires_at > $2;
