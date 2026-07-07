-- +goose Up
-- ==============================================================
-- 000004_agent_builtin_capabilities — 内置能力的 per-agent 关闭开关。
-- ==============================================================
-- 背景:
--   fetch_chat_history 这类"内置能力"由连接器在每次 prompt 时运行时注入
--   (携带 per-conversation 的 HMAC token),不走 capability/capability_version
--   的 DB 渲染路径,因此没有 agent_capabilities 绑定行可供开关。
--
--   前端需要把它当成"已安装、默认开启"的能力展示,并允许按 Agent 关闭。
--   这里只存最小的持久状态:一条 per-agent 的关闭标记。
--
-- 设计要点:
--   * 无行 = 开启(默认 ON)。写入 enabled=false 才表示该 Agent 关闭。
--   * capability_key 存内置能力的稳定标识(如 parsar_chat_history)。
--   * 纯 additive:不触碰 capability / capability_version / agent_capabilities,
--     对热路径与 daemon 契约零影响。
CREATE TABLE IF NOT EXISTS agent_builtin_capabilities (
  agent_id        uuid        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  capability_key  text        NOT NULL,
  enabled         boolean     NOT NULL DEFAULT true,
  created_at      timestamptz NOT NULL DEFAULT NOW(),
  updated_at      timestamptz NOT NULL DEFAULT NOW(),
  PRIMARY KEY (agent_id, capability_key)
);

COMMENT ON TABLE  agent_builtin_capabilities IS '内置能力的 per-agent 开关;无行=默认开启,enabled=false=该 Agent 关闭';
COMMENT ON COLUMN agent_builtin_capabilities.capability_key IS '内置能力稳定标识(如 parsar_chat_history)';
COMMENT ON COLUMN agent_builtin_capabilities.enabled        IS '是否为该 Agent 启用该内置能力';

-- +goose Down
DROP TABLE IF EXISTS agent_builtin_capabilities;
