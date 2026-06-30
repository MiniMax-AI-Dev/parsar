# 飞书 IM 路由与 Agent Visibility 设计

> 本文档定义 Parsar 飞书 IM 入口的产品语义与实现契约。
> 任何动飞书 webhook、Agent visibility、Gateway routing 或相关 RBAC 的实现 / 重构，必须先读本文档。
> 决策已经和产品负责人对齐（2026-05-29）；2026-06-09 更新：保留 per-Agent direct Bot，同时新增 env-backed 默认 shared Bot 命令路由模式。

---

## 1. 一句话定位

飞书 IM 入口是 Agent 在飞书的对外入口。当前支持两种 Bot routing mode：

- `direct`：默认模式，一个飞书 Bot 绑定一个 Agent，Bot 收到的消息直接进入该 Agent。
- `shared`：统一入口模式，一个实例级默认飞书 Bot 作为 host/router，用户通过 `/list` 查看可用 Agent，通过 `/select <编号或 slug>` 切换当前目标 Agent，普通消息进入已选 Agent。

无论哪种模式，消息都必须按目标 Agent 的 visibility 决定要不要回；对话内容永远落在目标 Agent 所在的 workspace，出站回复仍使用入站时的 Bot App ID。

它不是：

- 给 Parsar 用户做的「飞书登录入口」（那是 OAuth 平台应用的职责，见 §2）。
- 靠 `@<agent-name>` 文本随意 parse 的路由器（shared mode 只认明确命令和已保存选择）。
- 给 user 个人 workspace 当通知箱（conversation 归 Agent，不归 user）。

---

## 2. 两类飞书自建应用

飞书「自建应用」是一个容器，可同时承载 OAuth 和 Bot 能力。在 Parsar 里这两件事**产品语义完全不同**，必须分开建模。

| 类型 | 用途 | 数量 | 配置位置 |
| --- | --- | --- | --- |
| **OAuth 平台应用** | 给 Parsar 用户做飞书登录 | 一个 Parsar 实例固定 **1** 个 | env: `PARSAR_FEISHU_APP_ID/SECRET`（已实现，见 D.2 / `docs/deploy/feishu-prod.md`） |
| **Bot 应用（默认共享入口或 Agent 独立化身）** | shared：实例级默认命令入口；direct：一个 Agent 的收发消息入口 | 默认 shared Bot 一个实例 1 个；独立 Bot 可按 Agent 配多个 | 默认 shared Bot 读 env `PARSAR_FEISHU_DEFAULT_BOT_*`（未显式配置时回退 `PARSAR_FEISHU_APP_ID/SECRET`）；独立 Bot 写 `agents.config.connectors.feishu`（见 §6 schema） |

**为什么不合并**：

- OAuth 平台应用是「Parsar 平台」级别的资源，固定一组凭证，所有用户登录都走它。
- Bot 应用是「飞书入口」级别的资源：默认 shared 模式下实例 env 持有统一 Bot 凭证，目标 Agent 只负责执行与权限语义；direct 模式下通常 N 个 Agent 就 N 套凭证。
- 飞书侧也是分开的：OAuth 凭证泄露 → 影响平台登录；默认 shared Bot 凭证泄露 → 影响统一入口；独立 Bot 凭证泄露 → 只影响那个 Agent。

**OSS 偷懒模式**（开源版小团队部署，一个 Parsar 实例只挂 1 个 Agent 到飞书）：

- 允许把 OAuth 平台应用和 Bot 应用配成同一个飞书自建应用（App ID 填一样的值）。
- 这只是「配置层让两个独立字段填同一个值」，**架构层不能合并这两类**。
- 内部部署版多 Agent 场景下必须分开建。

---

## 3. Agent Visibility 三档

Agent 是 workspace 资产（`agents.workspace_id` 是归属，已实现），但「谁能调用这个 Agent」是另一个独立维度，由 `agents.visibility` 控制。

### 3.1 三档定义

| visibility | 谁能在 Web UI 调用 | 谁能在飞书 @ Agent bot | 典型场景 |
| --- | --- | --- | --- |
| **workspace**（默认） | 仅本 workspace 成员 | 仅本 workspace 成员的飞书账号 | 部门内部工具（团队专属 code reviewer / 内部数据查询 Agent） |
| **tenant** | 所有 Parsar 注册用户 | 所有 Parsar 注册用户的飞书账号 | 跨部门服务（HR 答疑 / IT 工单 / 内部知识 bot） |
| **public** | 所有 Parsar 注册用户 | 任何飞书用户（含未注册 Parsar 的飞书用户） | 对外服务（客服 / 销售 / 公开问答 bot） |

`tenant` 与 `public` 的差别**仅在飞书侧**：`tenant` 要求飞书发起人必须已经是 Parsar 用户（按飞书 `open_id` 查 `oauth_users` 命中），未注册的飞书用户会被 bot 引导去注册；`public` 直接放行未注册用户。

Web UI 调用层面 `tenant` 与 `public` 行为一致（都是「所有注册用户可见可用」），因为 Web 强制登录。

### 3.2 默认与约束

- **默认值**：`workspace`（最严格，守住安全底线）。
- **谁能改 visibility**：仅 workspace owner / admin（member 不行，防 member 私自把 Agent 放成 public 泄漏内部能力）。
- **`public` 改回 `workspace` / `tenant`**：允许，但 UI 必须二次确认，提示「该操作会让正在使用 Agent 的外部 / 跨租户用户立刻失去访问，历史对话仍保留」。
- **enum 收敛**：固定三档，不开 `restricted` / `team-internal` / `partner` 这种细分。要细分等真有客户场景再加。

### 3.3 Visibility 与 Conversation 归属的关系

**Visibility 决定「谁能发起对话」；Conversation 永远落在 Agent 所在的 workspace。**

这点至关重要，举例：

- Agent X (workspace=W1, visibility=tenant)
- Alice 是 Parsar 用户但只在 W2，不在 W1
- Alice 在飞书 @ X 的 bot 发问 → Bot 回（visibility=tenant 允许）→ 创建 conversation 在 **W1**（不是 W2），参与人记录 Alice
- Alice 在 Web 端能不能看到这个 conversation：另一个权限层（参与人能看自己参与的，详见 §7 RBAC）

---

## 4. 入站路由（飞书 → Parsar）

webhook / websocket 收到飞书消息时的处理顺序：

```text
1. 签名验证 / websocket tenant token 校验
2. challenge 响应             (URL 接入时)
3. 通过 inbound 的 app_id      → 若命中 env 默认 shared Bot app_id：使用虚拟 host route
                                  → 否则从 agents.config.connectors.feishu.app_id 找独立 Bot Agent
                                  → 两边都不命中则 400 Unknown app / websocket drop
4. 判断 routing mode
   - env 默认 Bot：固定 shared，先处理 /list /select；普通消息按 gateway_sessions(platform=feishu, external_id=chat_id, session_type=chat) 取 selected_agent_id
   - 独立 Bot direct：target Agent = host Agent
5. 通过 sender union_id/open_id → 查 Parsar user_id（可能不命中）
6. 按 target Agent.visibility 做 gate
7. 若 gate 通过 → 在 target Agent.workspace_id 里创建 / 续接 conversation
                  conversation.source_app_id = inbound app_id
                  触发 target Agent run
8. 若 gate 不通过 → host Bot 回引导文案，不创建 conversation
```

### 4.1 Visibility gate 详细逻辑

```text
visibility = workspace:
  sender_user_id ∈ workspace_members(agent.workspace_id) → 通过
  否则 → 拒（bot 回："此 Agent 仅 <workspace 名> 成员可用"）

visibility = tenant:
  sender_user_id 命中 oauth_users（即已注册 Parsar） → 通过
  否则 → 拒（bot 回："请先在 <Parsar URL> 注册后再使用"）

visibility = public:
  无条件通过
  若 sender_user_id 未命中 → conversation.participants 记 open_id, user_id=null
                              首条 bot 回复附「来 <URL> 注册可管理历史对话」
```

### 4.2 群聊场景

群聊里 @bot 时，路由依据**消息发起人的 open_id**（不是群 ID），其余跟单聊一致。

- 群聊不引入「群 → workspace 绑定」概念（避免管理员预先配置门槛）。
- 多人在群里 @ 同一个 Agent → 每个发起人各自一条 conversation，互不可见。
- 后续若有客户要「群即 conversation」语义，开 follow-up 设计，不在 MVP 里做。

### 4.3 未注册用户首登 reconcile

`visibility=public` 场景下，未注册飞书用户可以直接跟 bot 聊。当该用户后续真去 Parsar 注册（通过任何渠道）：

- 注册时拿到的飞书 `open_id` 与历史 conversation 的 `participants.open_id` 匹配
- 自动 reconcile：把历史 `participants.user_id` 从 null 回填为新建的 `user_id`
- 用户在 Web 端登录后可以看到自己历史飞书对话

### 4.4 Shared Bot 命令路由

默认 shared Bot 不直接执行业务请求；它由实例 env `PARSAR_FEISHU_DEFAULT_BOT_APP_ID` / `PARSAR_FEISHU_DEFAULT_BOT_APP_SECRET`（未显式配置时回退 `PARSAR_FEISHU_APP_ID` / `PARSAR_FEISHU_APP_SECRET`）提供飞书凭证，并维护飞书会话里的当前目标 Agent。

命令：

- `/list`：列出当前发送者可见的 Agent，并排除已启用独立 Bot 的 Agent。已登录 Parsar 的发送者可见 workspace / tenant / public Agent；未注册发送者只可见 public Agent。
- `/select <编号|agent-slug|workspace-slug/agent-slug>`：保存当前 `gateway_sessions(platform=feishu, external_id=chat_id, session_type=chat)` 的 `selected_agent_id`。
- `/help`：显示命令帮助。

普通消息：

1. 若没有 selection，host Bot 回复「请先 /list 再 /select」，不创建 conversation。
2. 若 selected Agent 已删除 / disabled，host Bot 提示重新选择。
3. 若 selected Agent 仍存在，必须再次跑 visibility gate；只有 gate 通过才调用 `CreateInboundIMMessage(TargetAgentID=selected_agent_id, SourceAppID=host_app_id)`。
4. 出站 worker 看到 `SourceAppID` 等于默认 Bot app_id 时直接使用 env 凭证，所以回复从统一 Bot 发出；审计 / AgentRun / usage 归目标 Agent。

---

## 5. 出站路由（Parsar → 飞书）

Agent 跑完产出消息后,按 conversation 来源决定回哪:

```text
conversation.source_gateway = feishu
conversation.source_app_id  = <Bot 应用的 App ID>
conversation.source_thread  = <飞书 reply anchor message_id,用于 reply_in_thread>
```

历史架构(B Phase, 已下线)有两条出站路径并存:
- **P1 outbound worker**: poll `messages.metadata.gateway_delivered_at=''` 的 agent 行,调 Feishu SendMessage,自带退避 / 死信。
- **P2 inflight driver**: poll `agent_run_events`,run.started 时发"执行中"卡, 事件流到来时 PATCH 同一张卡,run 完成时 PATCH 成 Done/Error 卡。

两条 loop 互相竞争同一个 conversation 时会出现"一条 query 两张卡"症状(P1 走完发了 Done 卡, P2 也独立 PATCH 一份);
所以 2026-06-12 重构把 P1 整个删了, **driver 现在是 Feishu 出站的唯一入口**(详见 `docs/feishu-driver-retry.md` 的 Driver-only architecture 一节)。

### 5.1 当前架构(driver-only)

Driver 守护进程做的事:

1. 每 ~2 s 调用 `ClaimActiveFeishuInflightConversations` 抢一批活跃 conversation
   (filter 条件:`agent_run_events.sequence > seq_emitted` OR `next_retry_at <= now` OR `gateway_delivered_at = ''`)。
2. 对每个 claim 到的 conversation 回放 `agent_run_events` 滚动出当前 card state(steps / streaming text / thinking)。
3. 没 working slot → POST 新的"执行中"卡, 把 message_id 写回 `gateway_inflight.working`;
   已有 working slot → PATCH 同一张卡。
4. 收到 `run.completed` / `run.failed` 事件时再 PATCH 一次成 Done / Error 卡, 调
   `MarkGatewayOutboundDelivered` 给 `messages.metadata.gateway_delivered_at` 盖戳,
   `ClearConversationInflightSlot` 清掉 working slot, 异步 DELETE typing reaction。
5. 任何一次 send / patch 失败按 `1s → 5s → 30s → 5m → 5m` 退避,第 6 次失败写 `sender_type='system'` 死信 notice + audit 行后清 slot。

凭证按 conversation.source_app_id 解析:等于默认 shared Bot env app_id → 用 env secret;否则从独立 Bot
`agents.config.connectors.feishu` 读 app_secret_ref 走 secret vault 解密。`source_thread` 入站时按
`root_id → parent_id → message_id` 取值,确保群聊和话题回复留在原消息线程里。

不要把独立 Bot 凭证缓存到 worker 进程内存——secret 旋转时无法热感知。每次发送时按需从 vault 取
(vault 自身有 LRU cache)。默认 shared Bot 凭证来自进程 env,轮换后需要重启服务或滚动发布。

### 5.2 Diagnostics 字段语义(Phase 6 起)

`GetFeishuConnectorDiagnostics` 的 outbound 计数现在从两处聚合(不再读 `messages.metadata.gateway_retry_*` 字段):

| 字段 | 来源 |
| --- | --- |
| `pending_outbound_count` | `messages.metadata.gateway_delivered_at = ''` 的 agent 行 |
| `delivered_outbound_count` | 同上但 `<> ''` |
| `retrying_outbound_count` | `conversations.metadata.gateway_inflight.working.attempts > 0` |
| `dead_outbound_count` | `sender_type='system'` 且 `metadata.kind LIKE 'feishu_outbound_dead_letter_%'` 的 message 数 |
| `last_error` / `last_error_at` | 最近一条 dead-letter notice 的 content/created_at, 否则当前 inflight slot 的 `last_error` |

---

## 6. Schema 改动清单

以下是飞书 IM 路由依赖的 schema / config 契约。已有 migration 的部分以数据库为准；jsonb connector 子结构是应用层约定。

### 6.1 agents 表新增 visibility column

```sql
alter table agents
  add column visibility text not null default 'workspace';

alter table agents
  add constraint agents_visibility_check
  check (visibility in ('workspace', 'tenant', 'public'));
```

**为什么是 column 不是 config jsonb**：

- RBAC 中间件 + 入站 routing gate 每次请求都查，高频路径。
- 需要 check constraint 锁 enum，jsonb 做不到强校验。
- 审计事件里要明文记录 visibility 变更（owner 把 Agent 改 public 是高敏感事件）。

**为什么不建索引**：`agents` 表按 workspace_id 查时自然 filter（workspace 表本身就小），visibility 单独建索引 ROI 低。

### 6.2 agents.config.connectors.feishu 子结构（约定，不是 schema）

默认 shared Bot 不写入任何 Agent 的 `agents.config.connectors.feishu`；它是实例级 env-backed 入口。这里的 jsonb 子结构只用于独立 Bot：选择「独立 Bot」的 Agent 持有自己的飞书 App ID 和 vault secret refs，并会从默认 Bot 的 `/list` 可选列表中排除。

`agents.config` 是 jsonb，但独立 Bot 的 feishu connector 配置部分约定如下结构：

```jsonc
{
  "connectors": {
    "feishu": {
      "enabled": true,
      "event_mode": "websocket",                 // websocket=扫码创建默认; webhook=手工配置兜底
      "routing_mode": "direct",                  // direct=一个 Bot 进一个 Agent; shared=/list+/select 统一入口
      "app_id": "cli_xxx",
      "app_secret_ref": "secret_id_in_vault",  // 不直接存 secret 明文
      "verification_token_ref": "secret_id_in_vault", // webhook 模式必填; websocket 模式可为空
      "encrypt_key_ref": "secret_id_in_vault",  // 可选，飞书事件加密开启时填
      "bot_open_id": "ou_xxx"                   // 可选, 用于 dedup 自己发的消息
    }
  }
}
```

`*_ref` 是 secret vault 里的引用 ID，明文凭证永远走 vault（D.6 + Phase 4 已建立的 secret 体系）。

扫码 provisioning 创建的 Bot 默认写 `event_mode="websocket"`，只需要 `app_secret_ref`；HTTP webhook 手工兜底路径使用 `event_mode="webhook"`，需要 `verification_token_ref` 做事件校验。

`routing_mode` 默认 `direct`。历史上允许 Agent connector 设置 `shared` 作为 host；当前默认 shared Bot 走实例 env，不要求也不建议为它创建 host Agent。独立 Bot 保存后会从默认 shared Bot 的 `/list` 中排除。

运行要求:server 启用 `PARSAR_FEISHU_WEBSOCKET=true` 后,websocket inbound manager 会启动 env 默认 shared Bot 长连接(若默认 Bot env 完整),并继续定期扫描 enabled 独立 Bot Agent 启动对应长连接;出站回复由 `PARSAR_FEISHU_OUTBOUND=true` 的 inflight driver 守护进程发送(env 名沿用了 P1 时代的命名作为别名)。

### 6.3 conversation 新增 source_app_id 字段

```sql
alter table conversations
  add column source_app_id text;
```

入站时填，用于出站时反查回对应的 Bot 应用凭证。已有的 `source_gateway` (`'feishu'` / `'web'` / ...) 字段配合使用。

### 6.4 飞书 inbound app_id → agent_id 查询

新加 store 方法（不需要新表，靠 agents.config.connectors.feishu.app_id 的 GIN 索引）：

```sql
create index idx_agents_feishu_app_id
  on agents ((config->'connectors'->'feishu'->>'app_id'))
  where deleted_at is null
    and (config->'connectors'->'feishu'->>'enabled')::boolean = true;
```

query：

```sql
select id, workspace_id, visibility
from agents
where config->'connectors'->'feishu'->>'app_id' = $1
  and deleted_at is null
  and (config->'connectors'->'feishu'->>'enabled')::boolean = true
limit 1;
```

### 6.5 gateway_sessions selection 表

`routing_mode=shared` 复用通用 Gateway session 状态，保存同一个外部平台会话当前选中的 Agent。飞书 shared Bot MVP 使用 chat 级选择态：一个飞书群 / 私聊会话共享一个 selected Agent。

```sql
create table gateway_sessions (
  id uuid primary key,
  platform text not null,
  external_id text not null,
  external_thread_id text not null default '',
  session_type text not null default 'chat',
  selected_agent_id uuid references agents(id) on delete set null,
  metadata jsonb not null default '{}'::jsonb,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  unique (platform, external_id, external_thread_id, session_type)
);
```

对飞书来说：`platform='feishu'`，`external_id=<chat_id>`，`external_thread_id=''`，`session_type='chat'`。`selected_agent_id` 删除时置空，下一条普通消息会提示用户重新 `/list` + `/select`。

---

## 7. RBAC 边界

### 7.1 Web 端查看 Agent / Conversation 的权限

- Agent 列表 / 详情可见性：
  - workspace member → 看本 workspace 全部 Agent（不论 visibility）
  - 非 workspace member 但 tenant 内：只看 `visibility in ('tenant','public')` 的 Agent
  - 未登录：只看 `visibility='public'` 的 Agent（开源版 default 实例可关闭这条）
- Conversation 详情可见性：
  - workspace member → 看本 workspace 全部 conversation
  - conversation 参与者（按 `participants.user_id` 命中）→ 看自己参与的
  - 其他人 → 不可见

### 7.2 谁能改 Agent visibility

- 仅 `workspace_members.role in ('owner','admin')` 可改
- `public` ↔ 其它档之间切换写 audit 事件 `agent.visibility.changed`

### 7.3 与 D.5 RBAC middleware 的关系

D.5 已经建立的 `requireWorkspaceMember` / `requireWorkspaceOwnerOrAdmin` 中间件不动，**新加一个 `requireAgentVisibilityAccess` 中间件**专门处理 Agent 相关 read 路径，按上面 7.1 规则 gate。

---

## 8. 不在 MVP 范围

明确不做：

- **群 → workspace 显式绑定配置**：workspace 始终由 target Agent 决定，shared mode 的 target Agent 由 chat-level `/select` 决定。
- **更细的 visibility 档**（`restricted` / `partner` 等）：要等真实客户场景。
- **靠 @ 文本 parse 的跨 Agent 路由**：shared mode 必须通过 `/list` + `/select`，不能靠自然语言或 `@<agent-name>` 猜目标。
- **复杂机器人命令平台**：MVP 只支持 `/list`、`/select`、`/help`，其余 slash 命令按普通 prompt 进入已选 Agent。
- **多语言文案 i18n**：bot 回复文案 MVP 用一份中文（默认）+ 一份英文（fallback）；正经 i18n 后续接入 Parsar 整体 i18n 时一起做。

---

## 9. 历史:B Phase 上线节奏

B Phase(飞书 IM 入站 / 出站 的初版实现)按 schema → 入站路由 → 出站 worker → visibility API/UI → OSS 偷懒模式 五段顺序上线。
出站部分(原 P1 outbound worker)已在 2026-06-12 driver-only 重构中整体替换,新做出站改动看 section 5.1 + `docs/feishu-driver-retry.md`。

---

## 10. 决策记录（why 选这套）

| 决策点 | 选了什么 | 没选什么 | 理由 |
| --- | --- | --- | --- |
| Bot 数量 | 默认一个 Agent 一个 Bot；另支持显式 shared Bot 命令路由 | 全实例隐式共用 Bot + @ parse | direct 保持清晰 Agent 化身；shared 满足少量统一入口诉求，但用 `/list` + `/select` 和 visibility gate 保持可治理 |
| Visibility 档数 | 三档（workspace/tenant/public） | 两档（private/public） | 内部部署版「全公司用但不对外」场景必然出现，预先建模比之后改 schema 便宜 |
| Conversation 归属 | Agent 所在 workspace | 发起人个人 workspace | 企业治理 / 审计 / 记忆需要集中在 Agent owner workspace |
| 飞书未注册用户 | bot 回 + 引导注册 | 静默丢弃 | 飞书侧静默不回 UX 灾难；`public` Agent 这个场景本来就是核心需求 |
| OAuth vs Bot 应用 | 双轨分离 | 合并成一个 | 凭证粒度 / 影响面 / 多 Agent 场景都需要分开；OSS 单 Agent 偷懒走配置层别名 |
| Visibility 字段位置 | agents 表 column | agents.config jsonb | 高频查询 + 需要 check constraint + 审计需要明文事件 |
| 改 visibility 权限 | owner / admin | 任意 member | 防 member 私自 public 泄漏 |
| 群聊路由 | 按发起人 open_id | 按群-workspace 绑定 | 简单，避免管理员预先配置门槛；后续真有客户需求再加 |

---

## 11. 相关文档

- 飞书 inflight driver 重试 / 死信 / 排查:`docs/feishu-driver-retry.md`
- 飞书生产环境部署:`docs/deploy/feishu-prod.md`
- Webhook 签名验证实现:`server/internal/auth/feishu/webhook.go`
- 数据模型 source of truth:`server/migrations/`(agents / conversations 表定义)
