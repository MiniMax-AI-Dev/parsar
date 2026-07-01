-- +goose Up
-- ==============================================================
-- 000004_teams_connector_platform — 放宽 platform CHECK,纳入 teams
-- ==============================================================
-- 背景:
--   000002 的内联 CHECK 只允许 feishu/slack/discord。Teams 复用同一张
--   workspace_im_connectors 表(列 app_id/enabled + jsonb config 里的
--   app_password_ref/tenant_id),无需新表;此处仅放宽平台白名单。
--   uk_wic_ws_platform / uk_wic_platform_appid / idx_wic_platform_enabled
--   均平台无关,自动覆盖 teams。
-- ==============================================================
ALTER TABLE workspace_im_connectors DROP CONSTRAINT workspace_im_connectors_platform_check;
ALTER TABLE workspace_im_connectors ADD CONSTRAINT workspace_im_connectors_platform_check
  CHECK (platform IN ('feishu', 'slack', 'discord', 'teams'));

COMMENT ON COLUMN workspace_im_connectors.platform IS 'IM 平台: feishu / slack / discord / teams';

-- +goose Down
ALTER TABLE workspace_im_connectors DROP CONSTRAINT workspace_im_connectors_platform_check;
ALTER TABLE workspace_im_connectors ADD CONSTRAINT workspace_im_connectors_platform_check
  CHECK (platform IN ('feishu', 'slack', 'discord'));

COMMENT ON COLUMN workspace_im_connectors.platform IS 'IM 平台: feishu / slack / discord';
