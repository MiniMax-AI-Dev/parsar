-- +goose Up
CREATE TABLE agent_mcp_tokens (
  agent_id uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (agent_id, user_id)
);

COMMENT ON TABLE agent_mcp_tokens IS
  'Personal, revocable credentials restricted to one Agent MCP endpoint; plaintext is never stored';

-- +goose Down
DROP TABLE agent_mcp_tokens;
