-- +goose Up
CREATE TABLE product_core_sessions (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspaces(id),
  conversation_id uuid NOT NULL REFERENCES conversations(id),
  agent_id uuid NOT NULL REFERENCES agents(id),
  request jsonb NOT NULL,
  core_session_id text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (conversation_id, agent_id)
);

CREATE TABLE product_core_runs (
  run_id uuid PRIMARY KEY REFERENCES agent_runs(id),
  session_binding_id uuid NOT NULL REFERENCES product_core_sessions(id),
  input jsonb NOT NULL,
  previous_turn_id text,
  core_turn_id text NOT NULL DEFAULT '',
  submitted boolean NOT NULL DEFAULT false,
  attempted boolean NOT NULL DEFAULT false,
  settled boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE product_core_runs;
DROP TABLE product_core_sessions;
