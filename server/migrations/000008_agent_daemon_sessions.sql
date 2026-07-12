-- +goose Up
CREATE TABLE IF NOT EXISTS agent_runtime_bindings (
  conversation_id uuid        NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  agent_id        uuid        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  runtime_id      uuid        NOT NULL REFERENCES runtimes(id) ON DELETE CASCADE,
  work_dir        text        NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_runtime_bindings_runtime
  ON agent_runtime_bindings(runtime_id);

CREATE TABLE IF NOT EXISTS agent_engine_sessions (
  conversation_id          uuid        NOT NULL,
  agent_id                 uuid        NOT NULL,
  agent_kind               text        NOT NULL,
  upstream_session_id      text        NOT NULL,
  upstream_session_type    text        NOT NULL,
  state_dir_key            text        NOT NULL,
  metadata                 jsonb       NOT NULL DEFAULT '{}'::jsonb,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, agent_id, agent_kind),
  FOREIGN KEY (conversation_id, agent_id)
    REFERENCES agent_runtime_bindings(conversation_id, agent_id)
    ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_engine_sessions_agent
  ON agent_engine_sessions(agent_id, agent_kind);

COMMENT ON TABLE  agent_runtime_bindings IS 'Agent-daemon conversation to runtime-device bindings';
COMMENT ON COLUMN agent_runtime_bindings.conversation_id IS 'Conversation using the runtime';
COMMENT ON COLUMN agent_runtime_bindings.agent_id        IS 'Agent using the runtime';
COMMENT ON COLUMN agent_runtime_bindings.runtime_id      IS 'Runtime device assigned to this conversation and agent';
COMMENT ON COLUMN agent_runtime_bindings.work_dir        IS 'Stable working directory for this conversation and agent';

COMMENT ON TABLE  agent_engine_sessions IS 'Agent-daemon upstream engine session state';
COMMENT ON COLUMN agent_engine_sessions.upstream_session_id   IS 'Agent-engine session identifier, e.g. Claude session id, Codex thread id, or Pi session id';
COMMENT ON COLUMN agent_engine_sessions.upstream_session_type IS 'Agent-engine session id type, e.g. claude_session, codex_thread, pi_session';
COMMENT ON COLUMN agent_engine_sessions.state_dir_key         IS 'Stable daemon-side state directory key for this conversation, agent, and engine';

-- +goose Down
DROP TABLE IF EXISTS agent_engine_sessions;
DROP TABLE IF EXISTS agent_runtime_bindings;
