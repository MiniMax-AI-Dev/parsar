-- +goose Up
-- ==============================================================
-- 000005_teams_connector_platform — Relax platform CHECK to include teams
-- ==============================================================
-- Background:
--   The inline CHECK from 000002 only allowed feishu/slack/discord. Teams
--   reuses the same workspace_im_connectors table (columns app_id/enabled
--   plus app_password_ref/tenant_id in the jsonb config), so no new table
--   is needed; this migration only relaxes the platform allowlist.
--   uk_wic_ws_platform / uk_wic_platform_appid / idx_wic_platform_enabled
--   are all platform-agnostic and automatically cover teams.
-- ==============================================================
ALTER TABLE workspace_im_connectors DROP CONSTRAINT workspace_im_connectors_platform_check;
ALTER TABLE workspace_im_connectors ADD CONSTRAINT workspace_im_connectors_platform_check
  CHECK (platform IN ('feishu', 'slack', 'discord', 'teams'));

COMMENT ON COLUMN workspace_im_connectors.platform IS 'IM platform: feishu / slack / discord / teams';

-- +goose Down
ALTER TABLE workspace_im_connectors DROP CONSTRAINT workspace_im_connectors_platform_check;
ALTER TABLE workspace_im_connectors ADD CONSTRAINT workspace_im_connectors_platform_check
  CHECK (platform IN ('feishu', 'slack', 'discord'));

COMMENT ON COLUMN workspace_im_connectors.platform IS 'IM platform: feishu / slack / discord';
