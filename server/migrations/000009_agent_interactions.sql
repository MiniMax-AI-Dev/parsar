-- +goose Up

CREATE TABLE agent_interactions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  agent_run_id    uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  request_id      text NOT NULL,
  kind            text NOT NULL CHECK (kind IN ('permission', 'user_choice')),
  status          text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'resolving', 'approved', 'denied', 'answered', 'cancelled', 'expired')),
  request         jsonb NOT NULL DEFAULT '{}'::jsonb,
  response        jsonb NOT NULL DEFAULT '{}'::jsonb,
  device_id       text NOT NULL DEFAULT '',
  claim_token     uuid,
  claimed_at      timestamptz,
  resolution_source text,
  resolved_actor  text,
  resolved_by     uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,
  resolved_at     timestamptz,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_agent_interactions_request UNIQUE (workspace_id, device_id, kind, request_id)
);

CREATE INDEX idx_agent_interactions_workspace_status_time
  ON agent_interactions(workspace_id, status, created_at DESC);

CREATE INDEX idx_agent_interactions_run_time
  ON agent_interactions(agent_run_id, created_at DESC);

CREATE INDEX idx_agent_interactions_expiry
  ON agent_interactions(expires_at)
  WHERE status = 'pending';

CREATE INDEX idx_agent_interactions_stale_claim
  ON agent_interactions(claimed_at)
  WHERE status = 'resolving';

COMMENT ON TABLE agent_interactions IS 'Durable human interaction requests shared by Web and IM surfaces';
COMMENT ON COLUMN agent_interactions.kind IS 'permission = approve or deny a tool action; user_choice = answer an AskUserQuestion request';
COMMENT ON COLUMN agent_interactions.status IS 'Lifecycle: pending, transient resolving, or terminal approved/denied/answered/cancelled/expired';
COMMENT ON COLUMN agent_interactions.request IS 'Immutable request snapshot rendered by Web and IM clients';
COMMENT ON COLUMN agent_interactions.response IS 'Human decision snapshot, populated only after resolution';
COMMENT ON COLUMN agent_interactions.device_id IS 'Agent-daemon device used to route a response to the pod owning the runtime WebSocket';
COMMENT ON COLUMN agent_interactions.claim_token IS 'CAS token held by the single resolver currently delivering a decision';
COMMENT ON COLUMN agent_interactions.resolution_source IS 'web, IM platform, system_timeout, or runtime';
COMMENT ON COLUMN agent_interactions.resolved_actor IS 'User UUID or external platform subject that submitted the decision';

-- +goose Down

DROP TABLE IF EXISTS agent_interactions;
