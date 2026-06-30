-- +goose Up
-- ==============================================================
-- 000002_workspace_im_connectors — workspace 维度的 IM 连接器绑定
-- ==============================================================
-- 背景:
--   共享 Bot 的绑定应当落在 workspace 维度(用户 @ 召唤机器人后从
--   /list 里挑选 Agent,同一 workspace 下所有 Agent 共享同一个 Bot
--   凭据),而不是 per-agent。历史上 Feishu 连接器配置被塞进
--   agents.config->'connectors'->'feishu',是 agent 维度;本表把三个
--   平台(feishu/slack/discord)统一收敛到 workspace 维度。
--
-- 设计:
--   * app_id 是 workspace-bot 的通用 join key —— 它在保存配置时即可知;
--     team_id(Slack)/guild_id(Discord)只有 Bot 入驻后才知道。
--   * 凭据密文仍存在 secrets(vault),本表 config 只存 *_ref(UUID 指针)
--     与非敏感字段(event_mode / intents 等)。
--   * 入站 reconciler 按 (workspace_id, app_id) 维持一条长连接;
--     出站 resolver 按 (platform, app_id) 反查解密 token。
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

-- 一个 workspace 每个平台至多一个有效连接器(共享 Bot 语义)。
CREATE UNIQUE INDEX IF NOT EXISTS uk_wic_ws_platform
  ON workspace_im_connectors(workspace_id, platform)
  WHERE deleted_at IS NULL;

-- 同一个 app_id 不能被两个 workspace 同时占用(入站路由唯一性)。
-- 仅对已填写 app_id 的有效行生效。
CREATE UNIQUE INDEX IF NOT EXISTS uk_wic_platform_appid
  ON workspace_im_connectors(platform, app_id)
  WHERE deleted_at IS NULL AND app_id <> '';

-- reconciler 按 platform 扫描所有启用的连接器。
CREATE INDEX IF NOT EXISTS idx_wic_platform_enabled
  ON workspace_im_connectors(platform)
  WHERE deleted_at IS NULL AND enabled = true;

COMMENT ON TABLE  workspace_im_connectors IS 'workspace 维度的 IM 连接器绑定(feishu/slack/discord 统一存储)';
COMMENT ON COLUMN workspace_im_connectors.id           IS '连接器主键';
COMMENT ON COLUMN workspace_im_connectors.workspace_id IS '所属 workspace';
COMMENT ON COLUMN workspace_im_connectors.platform     IS 'IM 平台: feishu / slack / discord';
COMMENT ON COLUMN workspace_im_connectors.app_id       IS '平台应用 ID(配置时即可知, workspace-bot 的通用 join key)';
COMMENT ON COLUMN workspace_im_connectors.enabled      IS '是否启用(启用后 reconciler 才会为其建立入站连接)';
COMMENT ON COLUMN workspace_im_connectors.config       IS '非敏感配置 + 密钥引用(*_ref 指向 secrets.id; 含 event_mode/intents 等)';
COMMENT ON COLUMN workspace_im_connectors.created_by   IS '创建人';
COMMENT ON COLUMN workspace_im_connectors.deleted_at   IS '软删除时间戳; NULL=有效';
COMMENT ON INDEX  uk_wic_ws_platform     IS '同一 workspace 每个平台至多一个有效连接器';
COMMENT ON INDEX  uk_wic_platform_appid  IS '同一 app_id 不能被两个 workspace 占用';
COMMENT ON INDEX  idx_wic_platform_enabled IS 'reconciler 按平台扫描启用的连接器';

-- +goose Down
DROP TABLE IF EXISTS workspace_im_connectors;
