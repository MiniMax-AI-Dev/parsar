-- +goose Up
-- ==============================================================
-- 000002_workspace_im_connectors — workspace-level IM connector bindings
-- ==============================================================
-- Background:
--   Shared-bot bindings belong at the workspace level (after a user @s
--   the bot they pick an Agent from /list, and every Agent in that
--   workspace shares one bot credential) rather than per-agent.
--   Historically the Feishu connector config was crammed into
--   agents.config->'connectors'->'feishu', which is per-agent; this
--   table folds all three platforms (feishu/slack/discord) into a
--   single workspace-level table.
--
-- Design:
--   * app_id is the universal join key for workspace-bot -- it is
--     known at config-save time; team_id (Slack) / guild_id (Discord)
--     are only knowable after the bot joins.
--   * Encrypted credentials still live in secrets (vault); this
--     table's config only stores *_ref (UUID pointers) plus
--     non-sensitive fields (event_mode / intents / ...).
--   * The inbound reconciler keeps one long-lived connection per
--     (workspace_id, app_id); the outbound resolver looks up tokens
--     via (platform, app_id).
-- ==============================================================
CREATE TABLE IF NOT EXISTS workspace_im_connectors (
  id           uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  platform     text NOT NULL
    CHECK (platform IN ('feishu', 'slack', 'discord')),
  app_id       text NOT NULL,
  enabled      boolean NOT NULL DEFAULT false,
  config       jsonb NOT NULL DEFAULT '{}',
  created_by   uuid REFERENCES users(id),
  created_at   timestamptz NOT NULL,
  updated_at   timestamptz NOT NULL,
  deleted_at   timestamptz
);

-- A workspace has at most one active connector per platform (shared-bot semantics).
CREATE UNIQUE INDEX IF NOT EXISTS uk_wic_ws_platform
  ON workspace_im_connectors(workspace_id, platform)
  WHERE deleted_at IS NULL;

-- The same app_id cannot be held by two workspaces at once (inbound routing uniqueness).
-- Only applies to active rows with a non-empty app_id.
CREATE UNIQUE INDEX IF NOT EXISTS uk_wic_platform_appid
  ON workspace_im_connectors(platform, app_id)
  WHERE deleted_at IS NULL AND app_id <> '';

-- The reconciler scans all enabled connectors by platform.
CREATE INDEX IF NOT EXISTS idx_wic_platform_enabled
  ON workspace_im_connectors(platform)
  WHERE deleted_at IS NULL AND enabled = true;

COMMENT ON TABLE  workspace_im_connectors IS 'Workspace-level IM connector bindings (feishu/slack/discord stored together)';
COMMENT ON COLUMN workspace_im_connectors.id           IS 'Connector primary key';
COMMENT ON COLUMN workspace_im_connectors.workspace_id IS 'Owning workspace';
COMMENT ON COLUMN workspace_im_connectors.platform     IS 'IM platform: feishu / slack / discord';
COMMENT ON COLUMN workspace_im_connectors.app_id       IS 'Platform app ID (known at config time; universal join key for workspace-bot)';
COMMENT ON COLUMN workspace_im_connectors.enabled      IS 'Whether enabled (only then does the reconciler establish an inbound connection)';
COMMENT ON COLUMN workspace_im_connectors.config       IS 'Non-sensitive config + secret references (*_ref points to secrets.id; includes event_mode/intents/etc.)';
COMMENT ON COLUMN workspace_im_connectors.created_by   IS 'Created by';
COMMENT ON COLUMN workspace_im_connectors.deleted_at   IS 'Soft-delete timestamp; NULL = active';
COMMENT ON INDEX  uk_wic_ws_platform     IS 'At most one active connector per platform per workspace';
COMMENT ON INDEX  uk_wic_platform_appid  IS 'The same app_id cannot be held by two workspaces';
COMMENT ON INDEX  idx_wic_platform_enabled IS 'Reconciler scans enabled connectors by platform';

-- +goose Down
DROP TABLE IF EXISTS workspace_im_connectors;
