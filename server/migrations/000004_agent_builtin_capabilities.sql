-- +goose Up
-- ==============================================================
-- 000004_agent_builtin_capabilities — Per-agent switch for built-in capabilities.
-- ==============================================================
-- Background:
--   "Built-in capabilities" such as fetch_chat_history are injected at runtime
--   by the connector on every prompt (carrying a per-conversation HMAC token).
--   They do not go through the capability/capability_version DB rendering path,
--   so there is no agent_capabilities binding row to toggle on/off.
--
--   The frontend needs to present them as "installed, on by default" and allow
--   turning them off per Agent. We persist only the minimum state: one
--   per-agent off flag.
--
-- Design points:
--   * No row = enabled (default ON). Writing enabled=false means this Agent
--     has turned it off.
--   * capability_key stores the stable identifier of the built-in capability
--     (e.g. parsar_chat_history).
--   * Purely additive: does not touch capability / capability_version /
--     agent_capabilities, and has zero impact on the hot path or the daemon
--     contract.
CREATE TABLE IF NOT EXISTS agent_builtin_capabilities (
  agent_id        uuid        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  capability_key  text        NOT NULL,
  enabled         boolean     NOT NULL DEFAULT true,
  created_at      timestamptz NOT NULL DEFAULT NOW(),
  updated_at      timestamptz NOT NULL DEFAULT NOW(),
  PRIMARY KEY (agent_id, capability_key)
);

COMMENT ON TABLE  agent_builtin_capabilities IS 'Per-agent switch for built-in capabilities; no row = default enabled, enabled=false = this Agent turned it off';
COMMENT ON COLUMN agent_builtin_capabilities.capability_key IS 'Stable identifier for the built-in capability (e.g. parsar_chat_history)';
COMMENT ON COLUMN agent_builtin_capabilities.enabled        IS 'Whether this built-in capability is enabled for this agent';

-- +goose Down
DROP TABLE IF EXISTS agent_builtin_capabilities;
