-- +goose Up
-- ==============================================================
-- 000001_init — squashed initial schema (2026-06-23, 第三次 squash)
-- ==============================================================
-- 历史:
--   * 2026-06-04 第一次 squash: 把上游 000001..000034 共 34 个 migration
--     压成单文件 000001_init。
--   * 2026-06-11 第二次 squash: 把第一次 squash 后追加的 000002..000007
--     (agent_daemon device owners / capability canonical / credential_kinds
--      UI 列清理 / spec_memory / gateway_sessions / capability_version
--      plugin 列) 以及一个被错放在顶层 migrations/ 目录从未真正执行过的
--      remove_admin_state 孤儿 migration 全部合进。
--   * 2026-06-23 第三次 squash(本文件): 把第二次 squash 后追加的
--     000004..000011 共 8 个 migration 合进:
--       - 000004 workspace_join_requests: workspaces.visibility +
--                workspace_members 状态机 (status/request_reason/
--                reviewed_by/reviewed_at) + 重建索引
--       - 000005 serialize_runs_cancel_stale: 一次性 UPDATE,空库 no-op,
--                **不再写入 init**
--       - 000006 project_agent_runtime_id: 运行时绑定列,现已并入 agents.runtime_id
--       - 000007 backfill_local_device_runtime_id: 一次性 UPDATE,空库
--                no-op,**不再写入 init**
--       - 000008 pending_credential_form_inflight_slot: conversations
--                gateway_inflight ADR-004 反查索引
--       - 000009 credential_kind_source: credential_kinds.source 列 +
--                CHECK 约束 + seed 直接带 source
--       - 000010 drop_project_members: 整段移除 project_members 表
--       - 000010 prompt_for_user_choice_inflight_slot: conversations
--                gateway_inflight AskUserQuestion 反查索引
--       - 000011 capability_pinning_mode: agent_capabilities.pinning_mode
--   合并后再次回到"单 init 文件"形态,上生产 (首次部署) 的 schema 跟代码
--   完全一致。
--
-- 为什么压:
-- 历史:
--   * 本仓库已与 upstream 脱钩,所有环境 (dev / staging / 首次
--     生产部署) 都走 DROP+重建,中间态对任何人都没有价值。
--   * 增量 ALTER/DROP 在审 schema 时是噪音,直接看最终 CREATE 更快。
--   * 历史细节由 git 保管,文件名简洁。
--
-- 未来 schema 变更新增 000002+ 增量 migration,本文件保持作为
-- 初始 schema 的 source of truth。
--
-- Down:
--   初始 migration 的 Down 等价于"清空数据库"。这里用最暴力的
--   DROP SCHEMA public CASCADE 而不是逐表 DROP —— 因为 goose down 到
--   v0 的语义本身就是 "回到一个干净的库",列 29 张 DROP TABLE 反而
--   容易因约束顺序写错而失败。

-- ============================================================
-- 表: users
-- ============================================================
CREATE TABLE IF NOT EXISTS users (
  id         uuid PRIMARY KEY,
  email      text UNIQUE NOT NULL,
  name       text NOT NULL DEFAULT '',
  status     text NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz
);

COMMENT ON TABLE  users IS '平台核心人员表';
COMMENT ON COLUMN users.id         IS '内部用户 ID';
COMMENT ON COLUMN users.email      IS '业务唯一标识';
COMMENT ON COLUMN users.name       IS '用户显示名';
COMMENT ON COLUMN users.status     IS '账号状态: active=正常 / disabled=被禁用';
COMMENT ON COLUMN users.created_at IS '注册时间';
COMMENT ON COLUMN users.updated_at IS '最近修改时间';
COMMENT ON COLUMN users.deleted_at IS '软删除时间戳; NULL=未删除';


-- ============================================================
-- 表: workspaces
-- ============================================================
CREATE TABLE IF NOT EXISTS workspaces (
  id         uuid PRIMARY KEY,
  name       text NOT NULL,
  slug       text UNIQUE NOT NULL,
  visibility text NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('public', 'private')),
  config     jsonb NOT NULL DEFAULT '{}',
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_workspaces_visibility_active
  ON workspaces(visibility)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  workspaces IS '团队级租户容器';
COMMENT ON COLUMN workspaces.id         IS 'workspace 内部 ID';
COMMENT ON COLUMN workspaces.name       IS '展示名';
COMMENT ON COLUMN workspaces.slug       IS 'URL/CLI 标识';
COMMENT ON COLUMN workspaces.visibility IS '可见性: public=可被其他用户发现并申请加入 / private=仅邀请';
COMMENT ON COLUMN workspaces.config     IS 'workspace JSON 配置';
COMMENT ON COLUMN workspaces.created_by IS '创建人';
COMMENT ON COLUMN workspaces.deleted_at IS '软删除时间戳; NULL=未删除';
COMMENT ON INDEX  idx_workspaces_visibility_active IS '发现端点按 visibility 筛选';


-- ============================================================
-- 表: workspace_members
--
-- status 状态机:
--   pending  — 用户自助申请,等待 owner/admin 审批
--   active   — 正式成员;所有 RBAC 查询只承认此状态
--   rejected — 申请被拒;保留行做审计,UNIQUE 索引排除它以便用户再申请
-- ============================================================
CREATE TABLE IF NOT EXISTS workspace_members (
  id             uuid PRIMARY KEY,
  workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id        uuid NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
  role           text NOT NULL,
  status         text NOT NULL DEFAULT 'active'
    CHECK (status IN ('pending', 'active', 'rejected')),
  request_reason text,
  reviewed_by    uuid REFERENCES users(id),
  reviewed_at    timestamptz,
  created_at     timestamptz NOT NULL,
  updated_at     timestamptz NOT NULL,
  deleted_at     timestamptz
);

-- 同一 workspace+user 仅一条非 rejected 的有效行;rejected 行保留做审计,
-- 不阻塞新 pending(用户被拒后可再次申请)。
CREATE UNIQUE INDEX IF NOT EXISTS uk_workspace_members_active
  ON workspace_members(workspace_id, user_id)
  WHERE deleted_at IS NULL AND status <> 'rejected';

-- 审批 UI 按 workspace 查待审批申请用。
CREATE INDEX IF NOT EXISTS idx_workspace_members_status_pending
  ON workspace_members(workspace_id, status)
  WHERE deleted_at IS NULL AND status = 'pending';

COMMENT ON TABLE  workspace_members IS 'workspace 成员与角色表(含申请状态机)';
COMMENT ON COLUMN workspace_members.workspace_id   IS '所属 workspace';
COMMENT ON COLUMN workspace_members.user_id        IS '所属用户';
COMMENT ON COLUMN workspace_members.role           IS '工作区角色: owner=拥有者 / admin=管理员 / member=普通成员 / viewer=只读';
COMMENT ON COLUMN workspace_members.status         IS '成员状态: pending=待审批 / active=正式成员 / rejected=申请被拒(保留行做审计)';
COMMENT ON COLUMN workspace_members.request_reason IS '用户提交申请时的理由(可空); active 行无意义';
COMMENT ON COLUMN workspace_members.reviewed_by    IS '审批人 user_id(同意或拒绝时填入)';
COMMENT ON COLUMN workspace_members.reviewed_at    IS '审批时间(同意或拒绝时填入)';
COMMENT ON COLUMN workspace_members.deleted_at     IS '软删除时间戳; NULL=当前有效成员';
COMMENT ON INDEX  uk_workspace_members_active           IS '同一 workspace+user 仅一条非 rejected 的有效行;rejected 行保留做审计,不阻塞新 pending';
COMMENT ON INDEX  idx_workspace_members_status_pending  IS '审批 UI 按 workspace 查待审批申请';


-- ============================================================
-- 表: auth_identities
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_identities (
  id         uuid PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider   text NOT NULL,
  subject    text NOT NULL,
  metadata   jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_auth_identities_provider_subject
  ON auth_identities(provider, subject);

COMMENT ON TABLE  auth_identities IS '用户外部身份绑定表';
COMMENT ON COLUMN auth_identities.user_id  IS '本地用户';
COMMENT ON COLUMN auth_identities.provider IS '身份提供方: email=本地邮箱密码 / feishu=飞书登录 / oidc=通用 OIDC';
COMMENT ON COLUMN auth_identities.subject  IS '外部唯一标识';
COMMENT ON COLUMN auth_identities.metadata IS '身份附加信息';
COMMENT ON INDEX  uk_auth_identities_provider_subject IS 'provider+subject 全局唯一';


-- ============================================================
-- 表: user_sessions
-- ============================================================
CREATE TABLE IF NOT EXISTS user_sessions (
  id           text PRIMARY KEY,
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_agent   text NOT NULL DEFAULT '',
  ip           text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_active
  ON user_sessions(user_id, expires_at DESC)
  WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at_brin
  ON user_sessions USING brin (expires_at);

COMMENT ON TABLE  user_sessions IS '用户登录会话表';
COMMENT ON COLUMN user_sessions.id           IS '会话 token';
COMMENT ON COLUMN user_sessions.user_id      IS '会话所属用户';
COMMENT ON COLUMN user_sessions.user_agent   IS '登录设备 UA';
COMMENT ON COLUMN user_sessions.ip           IS '登录来源 IP';
COMMENT ON COLUMN user_sessions.created_at   IS '会话创建时间';
COMMENT ON COLUMN user_sessions.last_seen_at IS '最近请求时间';
COMMENT ON COLUMN user_sessions.expires_at   IS '过期时间';
COMMENT ON COLUMN user_sessions.revoked_at   IS '主动注销时间; NULL=未撤销';
COMMENT ON INDEX  idx_user_sessions_user_active      IS '按用户查询有效会话';
COMMENT ON INDEX  idx_user_sessions_expires_at_brin  IS '按过期时间清理会话';


-- ============================================================
-- 表: user_credentials
-- ============================================================
CREATE TABLE IF NOT EXISTS user_credentials (
  id            uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind          text         NOT NULL,
  display_name  text         NOT NULL DEFAULT '',
  ciphertext    bytea        NOT NULL,
  key_version   text         NOT NULL DEFAULT 'v1',
  last_used_at  timestamptz,
  created_at    timestamptz  NOT NULL DEFAULT NOW(),
  updated_at    timestamptz  NOT NULL DEFAULT NOW(),
  deleted_at    timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_user_credentials_user_kind_active
  ON user_credentials(user_id, kind)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_credentials_user_active
  ON user_credentials(user_id)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  user_credentials IS '用户外部能力凭据表';
COMMENT ON COLUMN user_credentials.id           IS '凭据 ID';
COMMENT ON COLUMN user_credentials.user_id      IS '所属用户';
COMMENT ON COLUMN user_credentials.kind         IS '凭据类型; 由代码 registry 校验';
COMMENT ON COLUMN user_credentials.display_name IS '凭据展示名';
COMMENT ON COLUMN user_credentials.ciphertext   IS '加密后的凭据密文';
COMMENT ON COLUMN user_credentials.key_version  IS '加密密钥版本';
COMMENT ON COLUMN user_credentials.last_used_at IS '最近使用时间';
COMMENT ON COLUMN user_credentials.updated_at   IS '最近修改时间';
COMMENT ON COLUMN user_credentials.deleted_at   IS '软删除时间戳';
COMMENT ON INDEX  uk_user_credentials_user_kind_active IS '同一用户每种凭据仅一条有效记录';
COMMENT ON INDEX  idx_user_credentials_user_active     IS '按用户查询有效凭据';


-- ============================================================
-- 表: credential_kinds
-- ============================================================
-- 凭据类型注册表。源自 server/migrations/000003;原本硬编码在
-- server/internal/capability/credential_kind.go 的 5 种 kind 通过种子
-- 入库,后续在 capability 导入 UI 里管理员可 inline 新建。
--
-- source 分类:
--   * platform_oauth  — 平台代码内已实现 OAuth flow 的 provider
--                       (目前只有 github_pat;Slack/Feishu 等接入前保持 user_defined)
--   * platform_model  — LLM provider API key,模型目录在 models 表通过
--                       credential_mode=credential_ref 引用
--   * user_defined    — capability 导入流程里 admin 临时新增的 kind(默认值)
-- 应用代码读 source 把"连接"页拆成 OAuth 段 + 模型 API Key 段。
CREATE TABLE IF NOT EXISTS credential_kinds (
  id                uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  code              text         NOT NULL,
  display_name      text         NOT NULL,
  description       text         NOT NULL DEFAULT '',
  value_schema      jsonb        NOT NULL DEFAULT '{}'::jsonb,
  source            text         NOT NULL DEFAULT 'user_defined',
  built_in          boolean      NOT NULL DEFAULT FALSE,
  created_by        uuid         REFERENCES users(id),
  created_at        timestamptz  NOT NULL DEFAULT NOW(),
  updated_at        timestamptz  NOT NULL DEFAULT NOW(),
  deleted_at        timestamptz,
  CONSTRAINT credential_kinds_source_chk
    CHECK (source IN ('platform_oauth', 'platform_model', 'user_defined'))
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_credential_kinds_code_active
  ON credential_kinds(code)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_credential_kinds_active
  ON credential_kinds(code)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  credential_kinds IS '凭据类型注册表; 管理员可通过 capability 导入 UI 直接新建';
COMMENT ON COLUMN credential_kinds.id           IS 'credential kind 主键';
COMMENT ON COLUMN credential_kinds.code         IS '系统内唯一短码; 对应 user_credentials.kind 文本';
COMMENT ON COLUMN credential_kinds.display_name IS '展示名(中英文同栏); 至少非空';
COMMENT ON COLUMN credential_kinds.description  IS '类型说明; 展示给最终用户';
COMMENT ON COLUMN credential_kinds.value_schema IS 'value 校验 schema(预留); v1 不强校验';
COMMENT ON COLUMN credential_kinds.source       IS '分类: platform_oauth=平台内置 OAuth provider / platform_model=LLM provider API key / user_defined=管理员临时新增';
COMMENT ON COLUMN credential_kinds.built_in     IS '系统种子标记; built_in=true 不允许删除';
COMMENT ON COLUMN credential_kinds.created_by   IS '新建管理员; built_in=true 行此列 NULL';
COMMENT ON COLUMN credential_kinds.deleted_at   IS '软删除时间戳; NULL=活跃';
COMMENT ON INDEX  uk_credential_kinds_code_active IS 'code 在活跃记录中唯一';
COMMENT ON INDEX  idx_credential_kinds_active     IS '按 code 查询活跃 kind';

-- 种子: 把原硬编码在 server/internal/capability/credential_kind.go 的
-- 5 种内置 kind 写入注册表。built_in=TRUE 标记系统种子, UI 上不允许删。
-- ON CONFLICT DO NOTHING 保证 bundle 重复执行幂等。
INSERT INTO credential_kinds (code, display_name, description, source, built_in)
VALUES
  ('github_pat',         'GitHub 访问令牌',     'GitHub Personal Access Token', 'platform_oauth', TRUE),
  ('slack_bot_token',    'Slack Bot Token',     'Slack Bot Token (xoxb-…)',     'user_defined',   TRUE),
  ('postgres_dsn',       'Postgres 连接串',     'Postgres DSN',                 'user_defined',   TRUE),
  ('notion_integration', 'Notion 集成 token',   'Notion Integration Token',     'user_defined',   TRUE),
  ('jira_api_token',     'Jira API Token',      'Atlassian Jira API Token',     'user_defined',   TRUE)
ON CONFLICT DO NOTHING;


-- ============================================================
-- 表: secrets
-- ============================================================
-- 组织级共享加密凭据表。所有 kind 共用同一张表 + 同一套 secrets.Service 加密层。
-- 已知 kind:
--   model_provider     — 共享 model 的 API key（绑定到 models.secret_id）
--   runtime            — Sandbox provider 凭据（指针在 workspaces.config.runtime_credential_secret_id）
--   capability_inline  — MCP capability inline_secret（指针在 canonical_spec.env_value.secret_id）
--   feishu_bot         — 飞书 bot app secret
CREATE TABLE IF NOT EXISTS secrets (
  id                  uuid PRIMARY KEY,
  slug                text NOT NULL,
  name                text NOT NULL,
  kind                text NOT NULL DEFAULT 'model_provider',
  provider            text NOT NULL,
  auth_type           text NOT NULL,
  encrypted_payload   jsonb NOT NULL,
  key_version         text NOT NULL DEFAULT 'v1',
  metadata            jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_by          uuid REFERENCES users(id),
  created_at          timestamptz NOT NULL,
  updated_at          timestamptz NOT NULL,
  deleted_at          timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_secrets_slug_active
  ON secrets(slug)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_secrets_kind_active
  ON secrets(kind, status)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  secrets IS '组织级加密凭据表(共享 catalog)';
COMMENT ON COLUMN secrets.id                IS 'secret 主键';
COMMENT ON COLUMN secrets.slug              IS '机器可读的稳定标识(自动生成, 全局唯一)';
COMMENT ON COLUMN secrets.name              IS 'secret 展示名(可重复, 可修改)';
COMMENT ON COLUMN secrets.kind              IS 'secret 用途分类: model_provider/runtime/capability_inline/feishu_bot';
COMMENT ON COLUMN secrets.provider          IS '提供方标识';
COMMENT ON COLUMN secrets.auth_type         IS '认证类型';
COMMENT ON COLUMN secrets.encrypted_payload IS '加密后的凭证 payload';
COMMENT ON COLUMN secrets.key_version       IS '包封密钥版本';
COMMENT ON COLUMN secrets.metadata          IS '非敏感元数据';
COMMENT ON COLUMN secrets.status            IS '启用状态: active=可用 / disabled=管理员禁用';
COMMENT ON COLUMN secrets.created_by        IS '创建人';
COMMENT ON COLUMN secrets.deleted_at        IS '软删除标记; 非空表示已删除';
COMMENT ON INDEX  uk_secrets_slug_active IS 'secret slug 活跃唯一';
COMMENT ON INDEX  idx_secrets_kind_active IS '按 kind/status 筛选 secret';

-- ============================================================
-- 表: models
-- ============================================================
-- 组织级共享 model catalog (类 wiki/bulletin 语义)。
-- 所有用户可见可用; 只有创建者(或超管)可编辑/删除。
-- 没有 workspace_id — 全公司共用同一份目录。
-- provider 信息直接内联 (取代原 model_providers 中间表)。
-- 凭据二选一:
--   credential_mode='inline_secret'  → secret_id 引用 secrets 表(组织共享凭据)
--   credential_mode='credential_ref' → credential_kind_code 引用 credential_kinds.code
--                                       (运行时按 caller user_id + kind 查 user_credentials)
CREATE TABLE IF NOT EXISTS models (
  id                    uuid PRIMARY KEY,
  slug                  text NOT NULL,
  name                  text NOT NULL,
  provider_type         text NOT NULL,
  adapter               text NOT NULL,
  base_url              text NOT NULL DEFAULT '',
  model_key             text NOT NULL,
  credential_mode       text NOT NULL,
  secret_id             uuid REFERENCES secrets(id),
  credential_kind_code  text,
  config                jsonb NOT NULL DEFAULT '{}',
  status                text NOT NULL DEFAULT 'active',
  created_by            uuid REFERENCES users(id),
  created_at            timestamptz NOT NULL,
  updated_at            timestamptz NOT NULL,
  deleted_at            timestamptz,
  CONSTRAINT chk_models_credential_mode CHECK (
    (credential_mode = 'inline_secret' AND credential_kind_code IS NULL)
    OR (credential_mode = 'credential_ref'
        AND secret_id IS NULL
        AND credential_kind_code IS NOT NULL)
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_models_slug_active
  ON models(slug)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_models_status_active
  ON models(status)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_models_created_by_active
  ON models(created_by)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  models IS '组织级共享 LLM model catalog';
COMMENT ON COLUMN models.id                   IS '模型主键';
COMMENT ON COLUMN models.slug                 IS '机器可读稳定标识(自动生成, 全局唯一)';
COMMENT ON COLUMN models.name                 IS '模型展示名';
COMMENT ON COLUMN models.provider_type        IS 'provider 类型: anthropic / openai / ...';
COMMENT ON COLUMN models.adapter              IS 'opencode SDK adapter 包名(@ai-sdk/anthropic 等)';
COMMENT ON COLUMN models.base_url             IS 'API base URL';
COMMENT ON COLUMN models.model_key            IS 'provider 侧模型 ID';
COMMENT ON COLUMN models.credential_mode      IS '凭据模式: inline_secret(共享) / credential_ref(用户私有)';
COMMENT ON COLUMN models.secret_id            IS 'inline_secret 模式下绑定的共享 secret; NULL=待配置';
COMMENT ON COLUMN models.credential_kind_code IS 'credential_ref 模式下绑定的 credential_kinds.code';
COMMENT ON COLUMN models.config               IS '模型配置: capabilities/limits/headers/modalities/options 等';
COMMENT ON COLUMN models.status               IS '启用状态: active / disabled';
COMMENT ON COLUMN models.created_by           IS '创建人(用于编辑/删除权限校验)';
COMMENT ON COLUMN models.deleted_at           IS '软删除标记';
COMMENT ON INDEX  uk_models_slug_active        IS 'model slug 活跃唯一';
COMMENT ON INDEX  idx_models_status_active     IS '按 status 筛选 model';
COMMENT ON INDEX  idx_models_created_by_active IS '按创建人反查 model';


-- ============================================================
-- 表: runtimes
-- ============================================================
CREATE TABLE IF NOT EXISTS runtimes (
  id                       uuid PRIMARY KEY,
  workspace_id             uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  type                     text NOT NULL,
  name                     text NOT NULL,
  provider                 text NOT NULL,
  owner_user_id            uuid REFERENCES users(id) ON DELETE SET NULL,
  version                  text NOT NULL DEFAULT '',
  hostname                 text NOT NULL DEFAULT '',
  config                   jsonb NOT NULL DEFAULT '{}'::jsonb,
  pairing_token_hash       text,
  pairing_token_expires_at timestamptz,
  liveness                 text NOT NULL DEFAULT 'pending_pairing',
  last_heartbeat_at        timestamptz,
  created_at               timestamptz NOT NULL,
  updated_at               timestamptz NOT NULL,
  deleted_at               timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_runtimes_workspace_name_active
  ON runtimes(workspace_id, name)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_runtimes_workspace_type_liveness
  ON runtimes(workspace_id, type, liveness)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_runtimes_online_heartbeat
  ON runtimes(last_heartbeat_at)
  WHERE deleted_at IS NULL
    AND liveness = 'online';

CREATE INDEX IF NOT EXISTS idx_runtimes_pending_pairing
  ON runtimes(pairing_token_hash)
  WHERE deleted_at IS NULL
    AND liveness = 'pending_pairing'
    AND pairing_token_hash IS NOT NULL;

COMMENT ON TABLE  runtimes IS 'Agent runtime 注册表';
COMMENT ON COLUMN runtimes.id                       IS 'runtime 主键, 由 server 端生成';
COMMENT ON COLUMN runtimes.workspace_id             IS '所属 workspace';
COMMENT ON COLUMN runtimes.type                     IS 'runtime 类型: local=用户本机 Runner / sandbox=E2B 等远端沙盒 / external=外接 HTTP Agent';
COMMENT ON COLUMN runtimes.name                     IS 'runtime 名称';
COMMENT ON COLUMN runtimes.provider                 IS 'runtime provider';
COMMENT ON COLUMN runtimes.owner_user_id            IS '所属用户';
COMMENT ON COLUMN runtimes.version                  IS 'runner 版本号';
COMMENT ON COLUMN runtimes.hostname                 IS 'runner 主机名';
COMMENT ON COLUMN runtimes.config                   IS 'runtime 配置: runner_public_key/runner_credential_hash 等运行态配置(原 metadata 已并入)';
COMMENT ON COLUMN runtimes.pairing_token_hash       IS '配对令牌哈希';
COMMENT ON COLUMN runtimes.pairing_token_expires_at IS '配对令牌过期时间';
COMMENT ON COLUMN runtimes.liveness                 IS 'runtime 连通性: pending_pairing=待配对 / offline=配对后无心跳 / online=心跳正常 / error=runtime 自报故障';
COMMENT ON COLUMN runtimes.last_heartbeat_at        IS '最近心跳时间';
COMMENT ON COLUMN runtimes.deleted_at               IS '软删除标记; 非空表示已删除';
COMMENT ON INDEX  uk_runtimes_workspace_name_active  IS 'workspace 内 runtime 名称活跃唯一';
COMMENT ON INDEX  idx_runtimes_workspace_type_liveness IS '按 workspace/type/liveness 查询 runtime';
COMMENT ON INDEX  idx_runtimes_online_heartbeat      IS '扫描在线 runtime 心跳(sweeper 用)';
COMMENT ON INDEX  idx_runtimes_pending_pairing       IS '按配对令牌查待配对 runtime';


-- ============================================================
-- 表: agents
-- ============================================================
CREATE TABLE IF NOT EXISTS agents (
  id             uuid PRIMARY KEY,
  workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name           text NOT NULL,
  slug           text NOT NULL,
  description    text NOT NULL DEFAULT '',
  connector_type text NOT NULL,
  visibility     text NOT NULL DEFAULT 'workspace',
  status         text NOT NULL DEFAULT 'active',
  config         jsonb NOT NULL DEFAULT '{}',
  runtime_id     uuid REFERENCES runtimes(id) ON DELETE SET NULL,
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL,
  updated_at     timestamptz NOT NULL,
  deleted_at     timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_agents_workspace_slug_active
  ON agents(workspace_id, slug)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agents_feishu_app_id
  ON agents ((config->'connectors'->'feishu'->>'app_id'))
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agents_runtime_active
  ON agents(runtime_id)
  WHERE deleted_at IS NULL AND runtime_id IS NOT NULL;

COMMENT ON TABLE  agents IS 'workspace 级 Agent 定义表';
COMMENT ON COLUMN agents.id             IS 'Agent ID';
COMMENT ON COLUMN agents.workspace_id   IS '所属 workspace';
COMMENT ON COLUMN agents.name           IS 'Agent 展示名';
COMMENT ON COLUMN agents.slug           IS 'workspace 内 Agent 标识';
COMMENT ON COLUMN agents.description    IS 'Agent 描述';
COMMENT ON COLUMN agents.connector_type IS 'Agent 连接器类型';
COMMENT ON COLUMN agents.visibility     IS 'Agent 可见范围';
COMMENT ON COLUMN agents.status         IS '启用状态';
COMMENT ON COLUMN agents.config         IS 'Agent JSON 配置';
COMMENT ON COLUMN agents.runtime_id     IS '显式绑定的 runtime; NULL=未绑定(dispatch 时报错引导用户去 agent 设置页选择)';
COMMENT ON COLUMN agents.created_by     IS '创建人';
COMMENT ON COLUMN agents.created_at     IS '创建时间';
COMMENT ON COLUMN agents.updated_at     IS '最近更新时间';
COMMENT ON COLUMN agents.deleted_at     IS '软删除时间戳; NULL=活跃';
COMMENT ON INDEX  uk_agents_workspace_slug_active IS '同 workspace 内 Agent slug 活跃唯一';
COMMENT ON INDEX  idx_agents_feishu_app_id        IS '按飞书 app_id 反查 Agent';
COMMENT ON INDEX  idx_agents_runtime_active       IS '按 runtime 反查 agent 绑定(runtime 详情 / 删前 in-use 检查)';


-- ============================================================
-- 表: capability
-- ============================================================
CREATE TABLE IF NOT EXISTS capability (
  id              uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    uuid         NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  type            text NOT NULL,
  name            text         NOT NULL,
  description     text         NOT NULL DEFAULT '',
  tags            jsonb        NOT NULL DEFAULT '[]',
  visibility      text NOT NULL DEFAULT 'workspace',
  status          text NOT NULL DEFAULT 'active',
  creator_id      uuid         REFERENCES users(id),
  created_at      timestamptz  NOT NULL DEFAULT NOW(),
  updated_at      timestamptz  NOT NULL DEFAULT NOW(),
  deprecated_at   timestamptz,
  deleted_at      timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_capability_workspace_name_active
  ON capability(workspace_id, name)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_capability_workspace_type_active
  ON capability(workspace_id, type)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_capability_tags
  ON capability USING gin (tags);

COMMENT ON TABLE  capability IS 'Agent capability 目录表';
COMMENT ON COLUMN capability.id              IS '能力 ID';
COMMENT ON COLUMN capability.workspace_id    IS '所属 workspace';
COMMENT ON COLUMN capability.type            IS '能力类型: skill=opencode 内置工具脚本; mcp=标准 MCP server';
COMMENT ON COLUMN capability.name            IS '能力名称';
COMMENT ON COLUMN capability.description     IS '能力描述';
COMMENT ON COLUMN capability.tags            IS '分类标签(jsonb 字符串数组); 由 capability_tag 表合并而来';
COMMENT ON COLUMN capability.visibility      IS 'workspace=本 workspace 可见 / public=全平台可见。不含 tenant 层级——跨 workspace 共享走 marketplace 流程(000023)而非 visibility 字段';
COMMENT ON COLUMN capability.status          IS '能力启用状态';
COMMENT ON COLUMN capability.creator_id      IS '发布者';
COMMENT ON COLUMN capability.created_at      IS '发布时间';
COMMENT ON COLUMN capability.updated_at      IS '最近更新时间';
COMMENT ON COLUMN capability.deprecated_at   IS '软下线时间戳';
COMMENT ON COLUMN capability.deleted_at      IS '软删除时间戳';
COMMENT ON INDEX  uk_capability_workspace_name_active   IS 'workspace 内 capability 名称活跃唯一';
COMMENT ON INDEX  idx_capability_workspace_type_active  IS '按 workspace/type 查询 capability';
COMMENT ON INDEX  idx_capability_tags                   IS '按标签做 jsonb 包含查询(GIN)';


-- ============================================================
-- 表: capability_version
-- ============================================================
CREATE TABLE IF NOT EXISTS capability_version (
  id              uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  capability_id   uuid         NOT NULL REFERENCES capability(id) ON DELETE CASCADE,
  version         text         NOT NULL,
  git_repo_url    text,
  git_ref         text,
  path            text,
  content         jsonb,
  source_payload  jsonb,
  schema_version  smallint     NOT NULL DEFAULT 1,
  canonical_spec  jsonb,
  oss_key         varchar(512) NOT NULL DEFAULT '',
  sha256          varchar(64)  NOT NULL DEFAULT '',
  required_credentials jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(required_credentials) = 'array'),
  creator_id    uuid         REFERENCES users(id),
  created_at    timestamptz  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_capability_version_capability_version
  ON capability_version(capability_id, version);

CREATE INDEX IF NOT EXISTS idx_capability_version_capability
  ON capability_version(capability_id);

COMMENT ON TABLE  capability_version IS 'capability 版本表';
COMMENT ON COLUMN capability_version.id            IS '版本 ID';
COMMENT ON COLUMN capability_version.capability_id IS '所属 capability';
COMMENT ON COLUMN capability_version.version       IS '版本号';
COMMENT ON COLUMN capability_version.git_repo_url  IS '源码仓库 URL';
COMMENT ON COLUMN capability_version.git_ref       IS 'git tag 或 commit';
COMMENT ON COLUMN capability_version.path          IS '仓库内路径';
COMMENT ON COLUMN capability_version.content       IS '内联版本内容(per-scaffold rendered); 旧路径回退用';
COMMENT ON COLUMN capability_version.source_payload IS '导入时的原始粘贴内容快照; 形如 {"format":"json|toml|markdown","body":"…"}';
COMMENT ON COLUMN capability_version.schema_version IS 'canonical_spec 的 schema 版本; v1 起 = 1';
COMMENT ON COLUMN capability_version.canonical_spec IS '清洗后的规范化结构(canonical.Spec); Renderer 转 per-scaffold rendered;NULL 时 fallback content';
COMMENT ON COLUMN capability_version.oss_key IS 'Plugin 类型: 对象在 OSS bucket 内的 key;mcp/skill 类型为空字符串';
COMMENT ON COLUMN capability_version.sha256  IS 'Plugin 类型: zip 文件 SHA-256 摘要 (64 字符 hex);mcp/skill 类型为空字符串';
COMMENT ON COLUMN capability_version.required_credentials IS '该版本所需凭据清单(数组,快照); 元素形如 {kind, required, description}; kind 对应 user_credentials.kind, 由代码 registry 校验; 与本版本 content 里的 ${PARSAR_CREDENTIAL:<kind>} 占位符对应';
COMMENT ON COLUMN capability_version.creator_id    IS '版本发布者';
COMMENT ON COLUMN capability_version.created_at    IS '发布时间';
COMMENT ON INDEX  uk_capability_version_capability_version IS '同一 capability 下版本号唯一';
COMMENT ON INDEX  idx_capability_version_capability IS '按 capability 查询版本';

-- ============================================================
-- 表: agent_capabilities
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_capabilities (
  id                    uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id              uuid         NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  capability_id         uuid         NOT NULL REFERENCES capability(id) ON DELETE CASCADE,
  capability_version_id uuid         NOT NULL REFERENCES capability_version(id) ON DELETE RESTRICT,
  pinning_mode          text         NOT NULL DEFAULT 'pinned'
    CHECK (pinning_mode IN ('latest', 'pinned')),
  enabled               boolean      NOT NULL DEFAULT TRUE,
  configuration         jsonb        NOT NULL DEFAULT '{}'::jsonb,
  created_at            timestamptz  NOT NULL DEFAULT NOW(),
  updated_at            timestamptz  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_capabilities_agent_capability
  ON agent_capabilities(agent_id, capability_id);

CREATE INDEX IF NOT EXISTS idx_agent_capabilities_agent_active
  ON agent_capabilities(agent_id)
  WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_agent_capabilities_version
  ON agent_capabilities(capability_version_id);

COMMENT ON TABLE  agent_capabilities IS 'Agent capability 绑定表';
COMMENT ON COLUMN agent_capabilities.id                    IS '绑定记录 ID';
COMMENT ON COLUMN agent_capabilities.agent_id              IS '所属 agent';
COMMENT ON COLUMN agent_capabilities.capability_id         IS '绑定的 capability';
COMMENT ON COLUMN agent_capabilities.capability_version_id IS '锁定的版本; RESTRICT 防止误删仍被使用的版本';
COMMENT ON COLUMN agent_capabilities.pinning_mode          IS 'latest=dispatch 时查 capability 最新版本; pinned=锁 capability_version_id 列';
COMMENT ON COLUMN agent_capabilities.enabled               IS '绑定启用状态';
COMMENT ON COLUMN agent_capabilities.configuration         IS '能力实例配置';
COMMENT ON COLUMN agent_capabilities.created_at            IS '绑定时间';
COMMENT ON COLUMN agent_capabilities.updated_at            IS '最近修改时间';
COMMENT ON INDEX  uk_agent_capabilities_agent_capability IS '同一 Agent capability 绑定唯一';
COMMENT ON INDEX  idx_agent_capabilities_agent_active IS '按 Agent 查询启用 capability';
COMMENT ON INDEX  idx_agent_capabilities_version      IS '按版本反查 capability 使用方';


-- ============================================================
-- 表: agent_daemon_device_owners
-- ============================================================
-- agent_daemon WebSocket 设备 → owner pod 的租约表。源自
-- server/migrations/000002。一个 daemon device (runtime_id) 同一时刻
-- 只能被一个 pod 持有;generation 是 fencing token, renew / release
-- 必须匹配当前 generation, 防止陈旧 pod 误覆盖新 owner 的状态。
CREATE TABLE IF NOT EXISTS agent_daemon_device_owners (
  device_id        uuid        PRIMARY KEY REFERENCES runtimes(id) ON DELETE CASCADE,
  workspace_id     uuid        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  owner_pod_id     text        NOT NULL,
  owner_url        text        NOT NULL DEFAULT '',
  generation       bigint      NOT NULL DEFAULT 1,
  status           text        NOT NULL DEFAULT 'connected',
  connected_at     timestamptz NOT NULL DEFAULT now(),
  last_seen_at     timestamptz NOT NULL DEFAULT now(),
  lease_expires_at timestamptz NOT NULL,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT agent_daemon_device_owners_status_check
    CHECK (status IN ('connected', 'draining', 'expired'))
);

CREATE INDEX IF NOT EXISTS idx_agent_daemon_device_owners_owner_pod
  ON agent_daemon_device_owners(owner_pod_id);

CREATE INDEX IF NOT EXISTS idx_agent_daemon_device_owners_lease
  ON agent_daemon_device_owners(lease_expires_at);

COMMENT ON TABLE  agent_daemon_device_owners IS 'agent_daemon device_id 到当前 WebSocket owner pod 的租约表';
COMMENT ON COLUMN agent_daemon_device_owners.device_id        IS 'agent_daemon runtime/device id';
COMMENT ON COLUMN agent_daemon_device_owners.workspace_id     IS '设备所属 workspace';
COMMENT ON COLUMN agent_daemon_device_owners.owner_pod_id     IS '当前持有该 device WebSocket 的 pod id';
COMMENT ON COLUMN agent_daemon_device_owners.owner_url        IS '当前 owner pod 的内部可达 URL, 用于跨 pod 转发';
COMMENT ON COLUMN agent_daemon_device_owners.generation       IS 'fencing token; 每次 claim 递增, 续租/释放必须匹配';
COMMENT ON COLUMN agent_daemon_device_owners.status           IS 'owner 状态: connected / draining / expired';
COMMENT ON COLUMN agent_daemon_device_owners.connected_at     IS '本代 owner 连接建立时间';
COMMENT ON COLUMN agent_daemon_device_owners.last_seen_at     IS '当前 owner 最近续租/心跳时间';
COMMENT ON COLUMN agent_daemon_device_owners.lease_expires_at IS 'owner 租约过期时间';
COMMENT ON INDEX  idx_agent_daemon_device_owners_owner_pod IS '按 pod 列出其持有的所有 device(失联清理用)';
COMMENT ON INDEX  idx_agent_daemon_device_owners_lease     IS '按租约过期时间扫描过期 owner';


-- ============================================================
-- 表: sandboxes
-- ============================================================
CREATE TABLE IF NOT EXISTS sandboxes (
  id                             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id                   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  agent_id                       uuid REFERENCES agents(id) ON DELETE CASCADE,
  name                           text,
  cache_key                      text,
  sandbox_id                     text NOT NULL UNIQUE,
  template_id                    text NOT NULL,
  lifecycle_status               text NOT NULL DEFAULT 'running' CHECK (
    lifecycle_status IN (
      'spawning', 'running', 'renewing', 'killing', 'killed', 'killed_orphaned', 'killed_error'
    )
  ),
  allocation_status              text NOT NULL DEFAULT 'bound' CHECK (
    allocation_status IN ('pooled', 'bound', 'released')
  ),
  timeout_seconds                int NOT NULL DEFAULT 3600 CHECK (timeout_seconds > 0),
  auto_renew_threshold_seconds   int NOT NULL DEFAULT 0 CHECK (auto_renew_threshold_seconds >= 0),
  expires_at                     timestamptz,
  last_renewed_at                timestamptz,
  metadata                       jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at                     timestamptz NOT NULL DEFAULT now(),
  last_active_at                 timestamptz NOT NULL DEFAULT now(),
  killed_at                      timestamptz,
  CONSTRAINT sandboxes_bound_shape_check CHECK (
    allocation_status <> 'bound' OR (agent_id IS NOT NULL AND cache_key IS NOT NULL)
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_sandboxes_active_per_agent
  ON sandboxes(workspace_id, agent_id)
  WHERE allocation_status = 'bound'
    AND agent_id IS NOT NULL
    AND killed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandboxes_workspace_pool_available
  ON sandboxes(workspace_id, template_id, created_at)
  WHERE allocation_status = 'pooled'
    AND lifecycle_status = 'running'
    AND killed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandboxes_auto_renew_scan
  ON sandboxes(expires_at)
  WHERE killed_at IS NULL
    AND auto_renew_threshold_seconds > 0;

CREATE INDEX IF NOT EXISTS idx_sandboxes_workspace_active
  ON sandboxes(workspace_id, last_active_at DESC)
  WHERE killed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandboxes_created_at_brin
  ON sandboxes USING brin (created_at);

COMMENT ON TABLE  sandboxes IS '统一沙盒实例表。记录 workspace 范围内所有 provider 沙盒(如 E2B): 预热池沙盒(allocation_status=pooled)、已绑定到 agent 的持久沙盒(bound)、以及历史终止行(released)。不存 envd_access_token / endpoint URL 等敏感运行态信息; 这些只在进程内存中保存。';
COMMENT ON COLUMN sandboxes.id                             IS '沙盒实例主键';
COMMENT ON COLUMN sandboxes.workspace_id                   IS '归属 workspace; 预热池也按 workspace 隔离, 防止 credential / usage / permission 跨租户';
COMMENT ON COLUMN sandboxes.agent_id                       IS '绑定的 agent; pooled 行为空, bound 行必填';
COMMENT ON COLUMN sandboxes.name                           IS '沙盒人类可读名(可空), 主要用于 admin UI 展示';
COMMENT ON COLUMN sandboxes.cache_key                      IS '与 connector 的 buildPoolKey 输出对齐; pooled 行为空, bound 行必填';
COMMENT ON COLUMN sandboxes.sandbox_id                     IS '后端 provider(E2B 等)的真实沙盒 ID, 全局唯一';
COMMENT ON COLUMN sandboxes.template_id                    IS '模板 ID(E2B template_id), 决定沙盒镜像';
COMMENT ON COLUMN sandboxes.lifecycle_status               IS '生命周期: spawning=创建中 / running=可用 / renewing=续期中 / killing=终止中 / killed=已正常终止 / killed_orphaned=启动扫描清理 / killed_error=异常终止';
COMMENT ON COLUMN sandboxes.allocation_status              IS '归属状态: pooled=workspace 预热池可 claim / bound=已绑定到 agent / released=已释放历史行';
COMMENT ON COLUMN sandboxes.timeout_seconds                IS '沙盒续期秒数; Renew 后 provider 生命周期延长到 now + timeout_seconds';
COMMENT ON COLUMN sandboxes.auto_renew_threshold_seconds   IS '自动续期阈值: 0=关闭; >0=剩余生命周期低于该秒数时自动续期';
COMMENT ON COLUMN sandboxes.expires_at                     IS 'provider 侧当前生命周期到期时间; 自动续期扫描依赖此字段';
COMMENT ON COLUMN sandboxes.last_renewed_at                IS '最近一次续期成功或 claim handoff 时间';
COMMENT ON COLUMN sandboxes.metadata                       IS '审计上下文(spawn run_id、E2B 元数据、kill reason、source=pool/fresh 等), 不进入查询';
COMMENT ON COLUMN sandboxes.created_at                     IS '沙盒实例创建时间';
COMMENT ON COLUMN sandboxes.last_active_at                 IS '最近一次使用/状态更新时间(空闲 TTL 和 admin 列表依赖此字段)';
COMMENT ON COLUMN sandboxes.killed_at                      IS '终止时间; 非空表示沙盒已不可用, 仅作历史记录';
COMMENT ON CONSTRAINT sandboxes_bound_shape_check ON sandboxes IS 'bound 行必须有 agent_id 和 cache_key; pooled/released 可为空';
COMMENT ON INDEX  uk_sandboxes_active_per_agent            IS '同一 workspace 下, 每个 agent 同时最多只能有一个未 kill 的 bound 沙盒';
COMMENT ON INDEX  idx_sandboxes_workspace_pool_available   IS 'workspace-scoped pool claim 查询: 只扫描 running + pooled + 未 kill 的预热沙盒';
COMMENT ON INDEX  idx_sandboxes_auto_renew_scan            IS '自动续期扫描: 只索引启用 auto-renew 且未 kill 的沙盒';
COMMENT ON INDEX  idx_sandboxes_workspace_active           IS 'admin UI 在 workspace 范围内列当前活跃沙盒, 按最近活跃时间倒序';
COMMENT ON INDEX  idx_sandboxes_created_at_brin            IS 'BRIN 索引用于按 created_at 做时序扫描; 历史行会持续累积, BRIN 比 btree 更省空间';


-- ============================================================
-- 表: conversations
-- ============================================================
CREATE TABLE IF NOT EXISTS conversations (
  id                 uuid PRIMARY KEY,
  workspace_id       uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  surface            text NOT NULL DEFAULT 'web',
  form               text NOT NULL DEFAULT 'thread',
  title              text NOT NULL DEFAULT '',
  platform           text NOT NULL DEFAULT '',
  external_id        text NOT NULL DEFAULT '',
  external_thread_id text NOT NULL DEFAULT '',
  source_app_id      text NOT NULL DEFAULT '',
  status             text NOT NULL DEFAULT 'active',
  metadata           jsonb NOT NULL DEFAULT '{}',
  created_at         timestamptz NOT NULL,
  updated_at         timestamptz NOT NULL,
  deleted_at         timestamptz
);

CREATE INDEX IF NOT EXISTS idx_conversations_workspace_active
  ON conversations(workspace_id)
  WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uk_conversations_external_active
  ON conversations(workspace_id, platform, external_id, external_thread_id)
  WHERE deleted_at IS NULL
    AND platform <> ''
    AND external_id <> '';

-- ADR-004 反查索引:飞书 credential form submit callback 只携带 qkey,需要
-- O(log n) 反查出"哪个 conversation 挂着这个 pending form"。partial WHERE
-- 把索引体积压到"当前未填表单数"。
--
-- NB: 用 jsonb_exists(jsonb, text) 函数形式而不是 `jsonb ? text` 操作符 ——
-- 操作符里的 `?` 会被 JDBC / NineData 解析成参数占位符,在工单 / GUI 客户端
-- 提交时报 "No value specified for parameter 1"。函数形式语义完全等价,
-- 业务代码用 `?` 查询照样命中本索引(planner 知道两者等价)。
CREATE INDEX IF NOT EXISTS idx_conversations_pending_credential_form_qkey
  ON conversations(((metadata->'gateway_inflight'->'pending_credential_form'->>'qkey')))
  WHERE jsonb_exists(metadata->'gateway_inflight', 'pending_credential_form');

-- AskUserQuestion 反查索引:飞书 card_action callback 只携带 request_id
-- (button value 自带),需要 O(log n) 反查出对应 conversation。partial WHERE
-- 把索引体积压到"当前未回答 ask 卡片数"(一个 sharedbot 同时挂着的通常个位数)。
-- 同 idx_conversations_pending_credential_form_qkey 用 jsonb_exists() 而非 `?`。
CREATE INDEX IF NOT EXISTS idx_conversations_prompt_for_user_choice_request_id
  ON conversations(((metadata->'gateway_inflight'->'prompt_for_user_choice'->>'request_id')))
  WHERE jsonb_exists(metadata->'gateway_inflight', 'prompt_for_user_choice');

COMMENT ON TABLE  conversations IS '会话表';
COMMENT ON COLUMN conversations.id                 IS '会话 ID';
COMMENT ON COLUMN conversations.workspace_id       IS '所属 workspace';
COMMENT ON COLUMN conversations.surface            IS '会话顶层入口: web=内置 UI; im=即时通讯(具体平台见 platform 列); api=外部 API 触发';
COMMENT ON COLUMN conversations.form               IS '会话形态: thread=单线程对话(web 默认); group=群聊; dm=私聊; oneshot=一次性请求(api 默认)';
COMMENT ON COLUMN conversations.title              IS '会话标题';
COMMENT ON COLUMN conversations.platform           IS '外部平台标识';
COMMENT ON COLUMN conversations.external_id        IS '外部会话 ID';
COMMENT ON COLUMN conversations.external_thread_id IS '外部线程 ID';
COMMENT ON COLUMN conversations.source_app_id      IS '来源应用 ID';
COMMENT ON COLUMN conversations.status             IS '会话状态';
COMMENT ON COLUMN conversations.metadata           IS '会话元数据';
COMMENT ON COLUMN conversations.deleted_at         IS '软删除时间戳';
COMMENT ON INDEX  idx_conversations_workspace_active IS '按 Workspace 查询活跃会话';
COMMENT ON INDEX  uk_conversations_external_active IS '外部会话映射活跃唯一(workspace 内)';
COMMENT ON INDEX  idx_conversations_pending_credential_form_qkey IS 'ADR-004 反查: 飞书 credential form submit callback 只携带 qkey, 此 index 在 O(log n) 内解析到所属 conversation';
COMMENT ON INDEX  idx_conversations_prompt_for_user_choice_request_id IS 'AskUserQuestion 反查: 飞书 card_action callback 只携带 request_id, 此 index 在 O(log n) 内解析到所属 conversation';


-- ============================================================
-- 表: messages
-- ============================================================
CREATE TABLE IF NOT EXISTS messages (
  id              uuid PRIMARY KEY,
  workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  sender_type     text NOT NULL,
  sender_id       uuid,
  kind            text NOT NULL DEFAULT 'message',
  content_format  text NOT NULL DEFAULT 'text',
  visibility      text NOT NULL DEFAULT 'workspace',
  content         text NOT NULL DEFAULT '',
  metadata        jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL,
  updated_at      timestamptz NOT NULL,
  deleted_at      timestamptz
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation_time_active
  ON messages(conversation_id, created_at ASC)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_messages_workspace_time_active
  ON messages(workspace_id, created_at DESC)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  messages IS '会话时间线消息表';
COMMENT ON COLUMN messages.id              IS '消息 ID';
COMMENT ON COLUMN messages.workspace_id    IS '所属 workspace';
COMMENT ON COLUMN messages.conversation_id IS '所属会话';
COMMENT ON COLUMN messages.sender_type     IS '发送方类型: user=真人; agent=Agent 输出; system=Parsar 系统事件; external=外部 IM 用户(未注册)';
COMMENT ON COLUMN messages.sender_id       IS '对应 user_id 或 agent_id; system / external 可为空';
COMMENT ON COLUMN messages.kind            IS '消息语义类别: message=普通会话消息; artifact=产物消息; system_event=系统事件; error=错误。错误来源(agent/runtime/validation)放 metadata.error.source';
COMMENT ON COLUMN messages.content_format  IS '消息正文渲染格式: text=纯文本; markdown=Markdown; card=结构化卡片(schema 见 metadata.card)';
COMMENT ON COLUMN messages.visibility      IS '消息可见范围';
COMMENT ON COLUMN messages.content         IS '消息正文';
COMMENT ON COLUMN messages.metadata        IS '消息元数据';
COMMENT ON COLUMN messages.deleted_at      IS '软删除时间戳';
COMMENT ON INDEX  idx_messages_conversation_time_active IS '按会话时间查询消息';
COMMENT ON INDEX  idx_messages_workspace_time_active    IS '按工作区时间查询消息';


-- ============================================================
-- 表: agent_runs
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_runs (
  id                 uuid PRIMARY KEY,
  workspace_id       uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  conversation_id    uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  trigger_message_id uuid REFERENCES messages(id),
  trigger_source     text NOT NULL DEFAULT 'message',
  trigger_channel    text NOT NULL DEFAULT 'web',
  trigger_ref_type   text NOT NULL DEFAULT '',
  trigger_ref_id     uuid,
  requested_by_type  text NOT NULL,
  requested_by_id    uuid,
  agent_id           uuid NOT NULL REFERENCES agents(id),
  connector_type     text NOT NULL,
  external_run_id    text NOT NULL DEFAULT '',
  runtime_id         uuid REFERENCES runtimes(id) ON DELETE SET NULL,
  working_directory  text NOT NULL DEFAULT '',
  status             text NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled', 'interrupted')),
  visibility         text NOT NULL DEFAULT 'workspace',
  output_message_id  uuid REFERENCES messages(id),
  failure_reason     text NOT NULL DEFAULT '',
  metadata           jsonb NOT NULL DEFAULT '{}',
  created_at         timestamptz NOT NULL,
  started_at         timestamptz,
  finished_at        timestamptz,
  updated_at         timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_conversation_time
  ON agent_runs(conversation_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runs_workspace_status
  ON agent_runs(workspace_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_runs_agent_time
  ON agent_runs(agent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runs_trigger_message
  ON agent_runs(trigger_message_id);

CREATE INDEX IF NOT EXISTS idx_agent_runs_runtime_queue
  ON agent_runs(runtime_id, status, created_at)
  WHERE runtime_id IS NOT NULL;

COMMENT ON TABLE  agent_runs IS 'Agent 执行记录表';
COMMENT ON COLUMN agent_runs.id                 IS 'run ID';
COMMENT ON COLUMN agent_runs.workspace_id       IS '所属 workspace';
COMMENT ON COLUMN agent_runs.conversation_id    IS '所属会话';
COMMENT ON COLUMN agent_runs.trigger_message_id IS '触发消息 ID';
COMMENT ON COLUMN agent_runs.trigger_source     IS 'run 触发来源(WHAT): message=用户消息; agent=另一 agent; scheduled_task=定时任务; webhook=外部事件; issue=工单; manual=管理员手动';
COMMENT ON COLUMN agent_runs.trigger_channel    IS 'run 触发通道(HOW): web=内置 UI; im=即时通讯; api=外部 API; cron=定时调度; internal=系统内部';
COMMENT ON COLUMN agent_runs.trigger_ref_type   IS '触发源对象类型';
COMMENT ON COLUMN agent_runs.trigger_ref_id     IS '触发源对象 ID';
COMMENT ON COLUMN agent_runs.requested_by_type  IS '请求方类型';
COMMENT ON COLUMN agent_runs.requested_by_id    IS '请求方 ID';
COMMENT ON COLUMN agent_runs.agent_id           IS '本次 run 使用的 agent';
COMMENT ON COLUMN agent_runs.connector_type     IS '执行连接器类型快照';
COMMENT ON COLUMN agent_runs.external_run_id    IS '外部运行 ID';
COMMENT ON COLUMN agent_runs.runtime_id         IS '承载执行的 runtime';
COMMENT ON COLUMN agent_runs.working_directory  IS '本次 run 工作目录快照';
COMMENT ON COLUMN agent_runs.status             IS 'run 终态/过渡态: queued=入队; running=执行中; completed/failed=正常终态; cancelled=用户主动取消; interrupted=系统打断(如 runtime crash)';
COMMENT ON COLUMN agent_runs.visibility         IS 'run 可见范围';
COMMENT ON COLUMN agent_runs.output_message_id  IS '输出消息 ID';
COMMENT ON COLUMN agent_runs.failure_reason     IS '失败原因';
COMMENT ON COLUMN agent_runs.metadata           IS 'run 元数据';
COMMENT ON COLUMN agent_runs.created_at         IS '入队时间';
COMMENT ON COLUMN agent_runs.started_at         IS '开始执行时间';
COMMENT ON COLUMN agent_runs.finished_at        IS '终态时间';
COMMENT ON COLUMN agent_runs.updated_at         IS '任何字段变更时刷新';
COMMENT ON INDEX  idx_agent_runs_conversation_time   IS '按会话时间查询 run';
COMMENT ON INDEX  idx_agent_runs_workspace_status    IS '按 Workspace/status 查询 run';
COMMENT ON INDEX  idx_agent_runs_agent_time          IS '按 agent 时间查询 run';
COMMENT ON INDEX  idx_agent_runs_trigger_message     IS '按触发消息反查 run';
COMMENT ON INDEX  idx_agent_runs_runtime_queue       IS '按 runtime/status 查询待执行 run';


-- ============================================================
-- 表: connector_session_bindings
-- ============================================================
CREATE TABLE IF NOT EXISTS connector_session_bindings (
  id                  bigint       GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  conversation_id     text         NOT NULL,
  connector_type      text         NOT NULL,
  binding_key         text         NOT NULL,
  upstream_session_id text         NOT NULL,
  metadata            jsonb        NOT NULL DEFAULT '{}'::jsonb,
  created_at          timestamptz  NOT NULL DEFAULT now(),
  last_active_at      timestamptz  NOT NULL DEFAULT now(),
  CONSTRAINT uk_connector_session_bindings_conversation_connector_key
    UNIQUE (conversation_id, connector_type, binding_key)
);

CREATE INDEX IF NOT EXISTS idx_connector_session_bindings_connector_key
  ON connector_session_bindings (connector_type, binding_key);

COMMENT ON TABLE  connector_session_bindings IS '会话与上游 connector session 绑定表';
COMMENT ON COLUMN connector_session_bindings.id                  IS '绑定记录 ID';
COMMENT ON COLUMN connector_session_bindings.conversation_id     IS '会话 ID';
COMMENT ON COLUMN connector_session_bindings.connector_type      IS 'connector 类型, 如 opencode/claude_code/codex/http_agent';
COMMENT ON COLUMN connector_session_bindings.binding_key         IS 'connector 私有绑定 key, 如 OpenCode pool_key';
COMMENT ON COLUMN connector_session_bindings.upstream_session_id IS '上游 agent/connector session ID';
COMMENT ON COLUMN connector_session_bindings.metadata            IS 'connector 私有绑定元数据';
COMMENT ON COLUMN connector_session_bindings.created_at          IS '绑定建立时间';
COMMENT ON COLUMN connector_session_bindings.last_active_at      IS '最近复用时间';
COMMENT ON INDEX  idx_connector_session_bindings_connector_key   IS '按 connector 类型和绑定 key 查询 session 绑定';
COMMENT ON CONSTRAINT uk_connector_session_bindings_conversation_connector_key
  ON connector_session_bindings IS '保证同一会话、connector 类型和绑定 key 只对应一个上游 session';


-- ============================================================
-- 表: agent_run_events
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_run_events (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspaces(id),
  agent_run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  sequence     bigint NOT NULL,
  event_kind   text NOT NULL,
  payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at  timestamptz NOT NULL DEFAULT now(),
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_agent_run_events_run_sequence UNIQUE (agent_run_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_agent_run_events_run_seq
  ON agent_run_events(agent_run_id, sequence);

CREATE INDEX IF NOT EXISTS idx_agent_run_events_workspace_time
  ON agent_run_events(workspace_id, occurred_at DESC);

COMMENT ON TABLE  agent_run_events IS 'agent_run 流式事件表';
COMMENT ON COLUMN agent_run_events.id           IS '事件行主键';
COMMENT ON COLUMN agent_run_events.workspace_id IS '所属 workspace';
COMMENT ON COLUMN agent_run_events.agent_run_id IS '所属 run';
COMMENT ON COLUMN agent_run_events.sequence     IS 'run 内事件序号';
COMMENT ON COLUMN agent_run_events.event_kind   IS '事件类型';
COMMENT ON COLUMN agent_run_events.payload      IS '事件载荷';
COMMENT ON COLUMN agent_run_events.occurred_at  IS '事件发生时间';
COMMENT ON COLUMN agent_run_events.created_at   IS '落库时间';
COMMENT ON CONSTRAINT uk_agent_run_events_run_sequence ON agent_run_events IS '同一 run 内 sequence 唯一';
COMMENT ON INDEX  idx_agent_run_events_run_seq      IS '按 run/sequence 回放事件';
COMMENT ON INDEX  idx_agent_run_events_workspace_time IS '按 Workspace 时间查询事件';


-- ============================================================
-- 表: agent_run_artifacts
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_run_artifacts (
  id            uuid PRIMARY KEY,
  workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  agent_run_id  uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  name          text NOT NULL,
  medium        text NOT NULL DEFAULT 'file',
  kind          text NOT NULL DEFAULT '',
  uri           text NOT NULL DEFAULT '',
  visibility    text NOT NULL DEFAULT 'workspace',
  metadata      jsonb NOT NULL DEFAULT '{}',
  created_at    timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_run_artifacts_run
  ON agent_run_artifacts(agent_run_id);

CREATE INDEX IF NOT EXISTS idx_agent_run_artifacts_workspace_time
  ON agent_run_artifacts(workspace_id, created_at DESC);

COMMENT ON TABLE  agent_run_artifacts IS 'agent_run 产物表';
COMMENT ON COLUMN agent_run_artifacts.id            IS '产物 ID';
COMMENT ON COLUMN agent_run_artifacts.workspace_id  IS '所属 workspace';
COMMENT ON COLUMN agent_run_artifacts.agent_run_id  IS '所属 run';
COMMENT ON COLUMN agent_run_artifacts.name          IS '产物显示名';
COMMENT ON COLUMN agent_run_artifacts.medium        IS '产物载体: file=可下载文件; link=外链(URI 为完整 URL); inline=正文随 metadata 内联';
COMMENT ON COLUMN agent_run_artifacts.kind          IS '产物语义分类(free-form,不做 CHECK 约束): report / log / patch / pr_ref / image_thumbnail 等; ''=未分类';
COMMENT ON COLUMN agent_run_artifacts.uri           IS '产物 URI';
COMMENT ON COLUMN agent_run_artifacts.visibility    IS '产物可见范围';
COMMENT ON COLUMN agent_run_artifacts.metadata      IS '产物元数据';
COMMENT ON COLUMN agent_run_artifacts.created_at    IS '产物创建时间';
COMMENT ON INDEX  idx_agent_run_artifacts_run          IS '按 run 查询产物';
COMMENT ON INDEX  idx_agent_run_artifacts_workspace_time IS '按 Workspace 时间查询产物';


-- ============================================================
-- 表: usage_logs
-- ============================================================
CREATE TABLE IF NOT EXISTS usage_logs (
  id            uuid PRIMARY KEY,
  workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  agent_run_id  uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  provider      text NOT NULL DEFAULT '',
  model         text NOT NULL DEFAULT '',
  input_tokens  integer NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  cost_usd      numeric(12,6) NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
  raw           jsonb NOT NULL DEFAULT '{}',
  created_at    timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_logs_workspace_time
  ON usage_logs(workspace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_logs_run_time
  ON usage_logs(agent_run_id, created_at DESC);

COMMENT ON TABLE  usage_logs IS 'LLM 调用用量记录表';
COMMENT ON COLUMN usage_logs.id            IS '计费记录主键';
COMMENT ON COLUMN usage_logs.workspace_id  IS '所属 workspace';
COMMENT ON COLUMN usage_logs.agent_run_id  IS '关联 run';
COMMENT ON COLUMN usage_logs.provider      IS 'provider 标识';
COMMENT ON COLUMN usage_logs.model         IS '模型 key';
COMMENT ON COLUMN usage_logs.input_tokens  IS '输入 token 数, 非负';
COMMENT ON COLUMN usage_logs.output_tokens IS '输出 token 数, 非负';
COMMENT ON COLUMN usage_logs.cost_usd      IS '折算成本(USD)';
COMMENT ON COLUMN usage_logs.raw           IS '原始调用元数据';
COMMENT ON COLUMN usage_logs.created_at    IS '记录创建时间';
COMMENT ON INDEX  idx_usage_logs_workspace_time IS '按 Workspace/time 查询用量';
COMMENT ON INDEX  idx_usage_logs_run_time     IS '按 run/time 查询用量';


-- ============================================================
-- 表: audit_records
-- ============================================================
CREATE TABLE IF NOT EXISTS audit_records (
  id           bigserial    PRIMARY KEY,
  source       text         NOT NULL,
  event_type   text         NOT NULL,
  actor_type   text         NOT NULL,
  actor_id     uuid,
  target_type  text         NOT NULL DEFAULT '',
  target_id    uuid,
  workspace_id uuid         REFERENCES workspaces(id) ON DELETE CASCADE,
  payload      jsonb        NOT NULL DEFAULT '{}',
  occurred_at  timestamptz  NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_records_occurred_at_brin
  ON audit_records USING BRIN (occurred_at);

CREATE INDEX IF NOT EXISTS idx_audit_records_source_event_time
  ON audit_records (source, event_type, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_records_actor_time
  ON audit_records (actor_type, actor_id, occurred_at DESC)
  WHERE actor_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_records_target_time
  ON audit_records (target_type, target_id, occurred_at DESC)
  WHERE target_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_records_workspace_time
  ON audit_records (workspace_id, occurred_at DESC)
  WHERE workspace_id IS NOT NULL;

COMMENT ON TABLE  audit_records IS '合规审计流水表';
COMMENT ON COLUMN audit_records.id           IS '自增 ID';
COMMENT ON COLUMN audit_records.source       IS '审计来源分类';
COMMENT ON COLUMN audit_records.event_type   IS '审计事件类型';
COMMENT ON COLUMN audit_records.actor_type   IS '触发者类型';
COMMENT ON COLUMN audit_records.actor_id     IS '触发者 ID; system 事件可留空';
COMMENT ON COLUMN audit_records.target_type  IS '操作对象类型';
COMMENT ON COLUMN audit_records.target_id    IS '操作对象 ID';
COMMENT ON COLUMN audit_records.workspace_id IS '所属 workspace';
COMMENT ON COLUMN audit_records.payload      IS '脱敏事件上下文';
COMMENT ON COLUMN audit_records.occurred_at  IS '事件发生时间';
COMMENT ON INDEX  idx_audit_records_occurred_at_brin   IS '按时间范围扫描审计记录';
COMMENT ON INDEX  idx_audit_records_source_event_time  IS '按 source/event/time 查询审计记录';
COMMENT ON INDEX  idx_audit_records_actor_time         IS '按触发者查询审计记录';
COMMENT ON INDEX  idx_audit_records_target_time        IS '按操作对象查询审计记录';
COMMENT ON INDEX  idx_audit_records_workspace_time     IS '按 workspace 时间查询审计记录';


-- ==============================================================
-- 表: spec_fragments (来自 000005_spec_memory)
-- workspace 级 spec 片段,扁平多 fragment 结构(非文件树)。
-- 每条 fragment 独立可编辑/可注入/可写回,title+body+tags。
-- 三类写入来源由 source 字段区分,枚举集中在
-- server/internal/specmemory/types.go (Source/*),DB 层不加 CHECK IN
-- 约束,便于未来扩展新取值无需新 migration。
--   - 用户在 UI 写: source='manual',  created_by=<userID>, agent_actor=''
--   - agent CLI 写:  source='agent',   created_by=NULL,     agent_actor='<connector>:<agentID>'
--   - 文本导入:      source='import',  created_by=<userID>, agent_actor=''
-- ==============================================================
CREATE TABLE IF NOT EXISTS spec_fragments (
  id           uuid        PRIMARY KEY,
  workspace_id uuid        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  title        text        NOT NULL,
  body         text        NOT NULL,
  tags         text[]      NOT NULL DEFAULT '{}',
  source       text        NOT NULL,
  created_by   uuid        REFERENCES users(id),
  agent_actor  text        NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL,
  updated_at   timestamptz NOT NULL,
  deleted_at   timestamptz
);

CREATE INDEX IF NOT EXISTS idx_spec_fragments_workspace_active
  ON spec_fragments(workspace_id)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_spec_fragments_tags_active
  ON spec_fragments USING GIN(tags)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  spec_fragments IS 'workspace 级 spec 片段,每条独立可注入可编辑';
COMMENT ON COLUMN spec_fragments.id           IS 'fragment 内部 ID';
COMMENT ON COLUMN spec_fragments.workspace_id IS '所属 workspace';
COMMENT ON COLUMN spec_fragments.title        IS '片段标题';
COMMENT ON COLUMN spec_fragments.body         IS '片段 markdown 正文';
COMMENT ON COLUMN spec_fragments.tags         IS '标签数组,未来用于按 tag 智能注入';
COMMENT ON COLUMN spec_fragments.source       IS '来源类别 (manual/agent/import); 取值由 specmemory.Source 管理';
COMMENT ON COLUMN spec_fragments.created_by   IS '人工创建者 user_id; agent 写入时为 NULL';
COMMENT ON COLUMN spec_fragments.agent_actor  IS 'agent 写入时记录 connector:agentID; 人工创建时为空字符串';
COMMENT ON COLUMN spec_fragments.deleted_at   IS '软删除时间戳; NULL=未删除';
COMMENT ON INDEX  idx_spec_fragments_workspace_active IS '按 workspace 列出未删除 fragment';
COMMENT ON INDEX  idx_spec_fragments_tags_active      IS 'GIN 索引支持按 tag 过滤';


-- ==============================================================
-- 表: memories (来自 000005_spec_memory)
-- user/workspace 级 memory 共用一张表,通过 scope 区分。
-- memory_type 4 类: user/feedback/workspace/reference,枚举集中在
-- server/internal/specmemory/types.go (MemoryType/*),DB 层不加 CHECK IN
-- 约束。仅保留 scope ↔ workspace_id 的结构性约束防数据腐烂:
-- scope='user' 时 workspace_id 必须为 NULL,scope='workspace' 时必填。
-- conversation_id 在会话被删时置 NULL,memory 不连带删,以便审计追溯。
-- ==============================================================
CREATE TABLE IF NOT EXISTS memories (
  id              uuid        PRIMARY KEY,
  scope           text        NOT NULL,
  user_id         uuid        NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
  workspace_id    uuid        REFERENCES workspaces(id)        ON DELETE CASCADE,
  memory_type     text        NOT NULL,
  title           text        NOT NULL DEFAULT '',
  body            text        NOT NULL,
  why             text        NOT NULL DEFAULT '',
  tags            text[]      NOT NULL DEFAULT '{}',
  source          text        NOT NULL,
  agent_actor     text        NOT NULL DEFAULT '',
  conversation_id uuid        REFERENCES conversations(id)     ON DELETE SET NULL,
  created_at      timestamptz NOT NULL,
  updated_at      timestamptz NOT NULL,
  deleted_at      timestamptz,
  CONSTRAINT memories_scope_workspace_id_match_check
    CHECK ((scope = 'user'      AND workspace_id IS NULL)
        OR (scope = 'workspace' AND workspace_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_memories_user_scope_active
  ON memories(user_id, scope)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memories_workspace_active
  ON memories(workspace_id)
  WHERE deleted_at IS NULL AND scope = 'workspace';

CREATE INDEX IF NOT EXISTS idx_memories_tags_active
  ON memories USING GIN(tags)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  memories IS 'user/workspace 级 memory,agent 自觉写入 + 用户事后审计';
COMMENT ON COLUMN memories.id              IS 'memory 内部 ID';
COMMENT ON COLUMN memories.scope           IS '作用域 (user/workspace); 取值由 specmemory.Scope 管理';
COMMENT ON COLUMN memories.user_id         IS 'memory 归属用户; scope=workspace 时也填,用于排重与审计';
COMMENT ON COLUMN memories.workspace_id    IS '所属 workspace; scope=user 时为 NULL,scope=workspace 时必填';
COMMENT ON COLUMN memories.memory_type     IS '类型 (user/feedback/workspace/reference); 取值由 specmemory.MemoryType 管理';
COMMENT ON COLUMN memories.title           IS '简短标题,可选';
COMMENT ON COLUMN memories.body            IS '主体内容';
COMMENT ON COLUMN memories.why             IS 'feedback/workspace 类推荐填写的动因说明';
COMMENT ON COLUMN memories.tags            IS '标签数组';
COMMENT ON COLUMN memories.source          IS '来源 (user/agent/auto-review); 取值由 specmemory.Source 管理';
COMMENT ON COLUMN memories.agent_actor     IS 'agent 写入时记录 connector:agentID; 人工写入为空';
COMMENT ON COLUMN memories.conversation_id IS 'agent 写入时关联的会话 ID; 会话被删时置 NULL,不连带删 memory';
COMMENT ON COLUMN memories.deleted_at      IS '软删除时间戳; NULL=未删除';
COMMENT ON INDEX  idx_memories_user_scope_active IS '按用户+作用域列出未删除 memory';
COMMENT ON INDEX  idx_memories_workspace_active  IS '按 workspace 列出未删除 workspace 级 memory';
COMMENT ON INDEX  idx_memories_tags_active       IS 'GIN 索引支持按 tag 过滤';


-- ============================================================
-- 表: gateway_sessions (来自 000006_gateway_sessions)
-- Gateway 外部会话的路由选择状态。shared Feishu Bot 在群聊/私聊
-- 场景下,用户通过 /select 切换当前 Agent,选择持久化在这张表。
-- ============================================================
CREATE TABLE IF NOT EXISTS gateway_sessions (
  id                 uuid        PRIMARY KEY,
  platform           text        NOT NULL,
  external_id        text        NOT NULL,
  external_thread_id text        NOT NULL DEFAULT '',
  selected_agent_id  uuid        REFERENCES agents(id) ON DELETE SET NULL,
  metadata           jsonb       NOT NULL DEFAULT '{}'::jsonb,
  created_at         timestamptz NOT NULL,
  updated_at         timestamptz NOT NULL,
  CONSTRAINT gateway_sessions_key_required_check
    CHECK (btrim(platform) <> '' AND btrim(external_id) <> '')
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_gateway_sessions_external_scope
  ON gateway_sessions(platform, external_id, external_thread_id);

CREATE INDEX IF NOT EXISTS idx_gateway_sessions_selected_agent
  ON gateway_sessions(selected_agent_id)
  WHERE selected_agent_id IS NOT NULL;

COMMENT ON TABLE  gateway_sessions IS 'Gateway 外部会话的路由选择状态';
COMMENT ON COLUMN gateway_sessions.platform           IS '外部平台标识，如 feishu/slack/webhook';
COMMENT ON COLUMN gateway_sessions.external_id        IS '外部会话 ID，如飞书 chat_id';
COMMENT ON COLUMN gateway_sessions.external_thread_id IS '外部线程 ID；chat 级选择为空字符串';
COMMENT ON COLUMN gateway_sessions.selected_agent_id  IS '当前选中的 Parsar Agent';
COMMENT ON COLUMN gateway_sessions.metadata           IS 'Gateway session 元数据';
COMMENT ON INDEX  uk_gateway_sessions_external_scope  IS '同一外部平台会话范围仅保留一个当前选择';
COMMENT ON INDEX  idx_gateway_sessions_selected_agent IS '按当前选中 Agent 反查 session';

-- +goose Down
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
