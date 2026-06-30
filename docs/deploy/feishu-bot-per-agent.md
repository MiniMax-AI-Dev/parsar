# 飞书 Bot 接入指南（per-Agent / shared）

本文档面向把 Parsar Agent 暴露成飞书机器人的运维 / 项目管理员，覆盖两种接入方式：

- `direct`（默认）：一个飞书 Bot 绑定一个 Agent。
- `shared`：一个统一飞书 Bot 作为入口，用户在飞书里用 `/list`、`/select` 切换目标 Agent。

端到端流程仍然是：**飞书开放平台建 Bot 应用 → Parsar secret vault 灌凭证 → 在 Agent 详情页绑定 → 把 Bot 加进飞书群 → 群里 @ Bot 跑通**。

适用部署：开源版本和内部部署都一样。本流程不依赖任何 fork-only 代码。

> **架构边界（先读再做）**
>
> Parsar 把飞书"自建应用"分两类，对应不同 Parsar 资源：
>
> | 应用类型 | 用途 | 配置位置 | 数量 |
> | --- | --- | --- | --- |
> | **OAuth 平台应用** | Parsar 用户用飞书账号登录 Web 后台 | env `PARSAR_FEISHU_APP_ID/SECRET` | 一个 Parsar 实例固定 1 个 |
> | **Bot 应用（本文档主题）** | direct：一个 Agent 的飞书入口；shared：统一命令入口 | `agents.config.connectors.feishu` | 默认每个挂飞书的 Agent 一个，也可配置共享 host Bot |
>
> 这两件事产品语义完全不同，必须分开建（凭证泄露面、scope、生命周期都不同）。
> 详见 `docs/feishu-routing.md §2`。
>
> **OAuth 应用如何建** → 看 `docs/deploy/feishu-prod.md`，**不在本文档范围**。
> 本文档只讲 Bot 应用。

---

## 0. 前置条件

开干前确认：

- [ ] Parsar server 已经 deploy 并通过 `smoke.sh --core`（详见 `deploy-runbook.md`）
- [ ] 你已经用飞书账号登录 Parsar Web，并且是目标 workspace 的 **owner / admin**
  （Bot 配置 RBAC 限制 owner / admin；普通 member 看不到表单）
- [ ] 已经创建至少一个 Agent（connector_type = agent_daemon 或 http）
- [ ] 你在飞书租户里有创建自建应用的权限（一般 IT 管理员开通；个人 lark.com 也行）
- [ ] Parsar server 对外有可访问 HTTPS 域名（飞书事件订阅强制 HTTPS）

如果上面任一项缺，先回 `deploy-runbook.md` / `feishu-prod.md` 补齐。

---

## 1. 飞书开放平台：建 Bot 自建应用

### 1.1 创建应用

1. 打开 [飞书开放平台](https://open.feishu.cn/app)（lark.com 用户用 `https://open.larksuite.com/app`）。
2. 「创建企业自建应用」→ 填名称（建议带 Agent 名，如 `Parsar · 产品答疑 Bot`）+ 简介 + 图标。
3. **不要**复用 OAuth 平台应用的那一个。你要的就是另起炉灶的一个新应用。

### 1.2 记录基础凭证

应用创建后进入「凭证与基础信息」页，**记录 4 个值**（待会儿要灌进 Parsar）：

| 飞书后台名称 | Parsar 字段 | 备注 |
|---|---|---|
| App ID | `app_id` | 形如 `cli_a1b2c3d4e5f6g7h8` |
| App Secret | App Secret（保存时自动写入 secret vault 并生成 `app_secret_ref`） | **只显示一次**，立刻拷下来 |
| Verification Token | Verification Token（保存时自动写入 `verification_token_ref`） | 1.4 节配置事件订阅时拿 |
| Encrypt Key（可选） | Encrypt Key（保存时自动写入 `encrypt_key_ref`） | 1.4 节如果开启事件加密才有 |

> **永远不要把这 4 个值 commit 进任何 repo 或 docs 默认值。** 它们走 Parsar
> secret vault（运行时加密 + 审计），明文只在你本地剪贴板里短暂存在。

### 1.3 申请权限（scope）

进「权限管理」页，**至少申请**：

| Scope | 用途 |
|---|---|
| `im:message` | 接收 IM 消息事件 |
| `im:message.group_at_msg:readonly` | 接收群里 @Bot 的消息 |
| `im:message.p2p_msg:readonly` | 接收 p2p 私聊消息（如果想 1:1 也能聊） |
| `im:message:send_as_bot` | 以 Bot 身份发消息 |
| `im:chat:readonly` | 读群信息（出站发消息要 chat_id） |
| `contact:user.id:readonly` | 把消息发送人 open_id 映射到 union_id（visibility gate 需要） |

申请后**必须发布版本并让租户管理员审批**，否则 scope 不生效。

> **企业管理员通常不会立刻批。** 提交后跟 IT 同事打个招呼加速。审批不通过这条链路就废了。

### 1.4 开启「机器人」能力

进「应用能力 → 机器人」，**点击开启**。

> 飞书"自建应用"默认只是个 OAuth 容器；不开机器人能力的话 Bot 没法被加进群、@Bot 也收不到事件。**这是最常被漏的一步**。

### 1.5 配置事件订阅

进「事件订阅」页：

1. **请求地址**：填 `https://<your-parsar-domain>/api/v1/feishu/events/message`
   - 必须 HTTPS，飞书拒绝 HTTP
   - 路径**就是 `/api/v1/feishu/events/message`**，不要改名
2. **Verification Token**：飞书会自动生成。**拷下来**——下面 §3 的绑定卡片会自动存进 secret vault
3. **加密（可选）**：如果点开「事件加密」，会生成 Encrypt Key——**拷下来**
4. 点「保存」时飞书会发一个 `url_verification` 挑战，Parsar 内置 handler 自动回 `{"challenge":"..."}`
   - 失败时排查：见 `feishu-prod.md §6.1 / §6.2`
5. **订阅事件**：在事件列表里至少订阅
   - `im.message.receive_v1`（接收消息事件，最重要）

> 订阅事件后**一定要再次发布版本**让租户管理员审批，否则订阅不生效。

---

## 2. Parsar：Secret Vault 保存规则

不需要先去「Secrets」页手动创建这些条目。Agent 详情页的飞书 Bot 绑定卡片会在保存时把 App Secret / Verification Token / Encrypt Key 自动写入 Secret Vault，并且只把 `app_secret_ref` / `verification_token_ref` / `encrypt_key_ref` 存进 Agent 配置。

> Secret vault 用 `PARSAR_MASTER_KEY` 加密；vault 里只存密文，UI 永远不回显明文（只显示 masked 预览）。

自动创建的 Secret 使用以下约定（Encrypt Key 可选）：

| kind | provider | payload 字段 |
|---|---|---|
| `feishu_app_secret` | `feishu` | `app_secret` |
| `feishu_verification_token` | `feishu` | `verification_token` |
| `feishu_encrypt_key` *(仅事件加密开启时)* | `feishu` | `encrypt_key` |

后续轮换时，在绑定卡片里重新输入新值并保存即可；留空表示继续使用当前已保存的 Secret。

---

## 3. Parsar：在 Agent 详情页绑定 Bot

进「Agents」页（`?admin=agents`），选你要挂 Bot 的 Agent → **Connector tab**。

下面会看到两块：

- 上面：现有的 connector 信息（agent_daemon / http）
- 下面：**「飞书 Bot 绑定」卡片**

在「飞书 Bot 绑定」卡片填：

| 字段 | 值来源 | 必填 |
|---|---|---|
| Bot 接入 | 选择「默认 Bot」或「独立 Bot」 | 默认 Bot 不需要单独填写飞书应用 |
| App ID | 1.2 节的 App ID | 独立 Bot 启用时必填 |
| App Secret | 1.2 节的 App Secret；保存时自动存成 Secret | 没有已保存值时必填 |
| Verification Token | 1.5 节拷的 Verification Token；保存时自动存成 Secret | webhook 模式且没有已保存值时必填 |
| Encrypt Key（可选） | 1.5 节拷的 Encrypt Key；保存时自动存成 Secret | 仅事件加密开启时填 |
| Bot Open ID（可选） | 留空，第一次跑通后再回填（见 §5） | 选填 |

点「保存」。选择「独立 Bot」后，这个 Agent 会从默认 Bot 的可选列表中移除。

**可能的错误**：

| 错误 | 含义 | 解决 |
|---|---|---|
| `feishu_connector_incomplete`（422） | 勾了启用但有必填没填 | 检查 App ID + App Secret + Verification Token |
| `feishu_app_id_in_use`（409） | 这个 App ID 已经绑在本实例的另一个启用 Agent 上 | 换 App ID，或先把那个 Agent 的飞书绑定 disable |
| 红字"无可用 Secret" | 当前 workspace 没有 active Secret | 回 §2 创建 Secret |

保存成功后 audit 会落一条 `agent.feishu_connector.updated`。

---

## 4. 飞书租户：把 Bot 加进群（或 1:1）

### 4.1 群聊

1. 在飞书任意群点设置 → 群机器人 → 添加机器人
2. 搜你 §1.1 建的 Bot 名 → 添加
3. direct 模式：群成员 **@Bot** 发消息 → 应该被 Parsar 收到 → 触发当前 Agent run → Bot 回消息
4. shared 模式：群成员先 **@Bot /list**，再 **@Bot /select <编号或 slug>**，之后普通消息进入选中的 Agent → Bot 回消息

### 4.2 1:1 私聊

1. 飞书全局搜 Bot 名 → 直接发消息（部分租户管理员策略可能禁用，看 IT 配置）

> **Agent visibility 必须 ≥ 发起人覆盖范围**：
>
> - `visibility=workspace`（默认）：只接受该 workspace 成员的 @Bot
> - `visibility=tenant`：所有已注册 Parsar 用户都能 @Bot
> - `visibility=public`：任何飞书用户都能 @Bot（含未注册）
>
> 在 Agent 详情页的 Overview tab 改 visibility（owner/admin 权限）。
> 详见 `docs/feishu-routing.md §3`。

### 4.3 群里 @Bot 不回的常见原因

| 现象 | 原因 | 排查 |
|---|---|---|
| @Bot 完全无反应，Parsar 没收到 webhook | 事件订阅 URL 不通 / scope 没审批 | 飞书后台「事件订阅」点测试连接；检查租户管理员是否审批了 `im:message` scope |
| Parsar 收到 webhook 但日志说 `unknown app_id` | App ID 没写进 Agent 配置 / Agent 没启用 / Agent 已删 | 检查 Agent 详情页的 `enabled` 勾选；DB 里 `select config->'connectors'->'feishu' from agents where id=...` |
| Parsar 收到 webhook + 路由到了 Agent 但 visibility 拒绝 | 发起人不在 visibility 范围 | 检查 Agent visibility + 发起人是否已注册 Parsar |
| Agent 跑了但 Bot 没回 | 出站 worker 没起来 / 凭证错 / 飞书 API 报错 | server 日志 grep `inflight`；检查 `PARSAR_FEISHU_OUTBOUND=true` env 是否打开 |

---

## 4.4 AgentRun 回写验收

真实飞书端 E2E 时，除了确认 Parsar 收到入站事件，还要确认 AgentRun 结果会回到同一条飞书消息 thread：

| 场景 | 期望 |
|---|---|
| Agent 正常输出文本 | Bot 在原群聊 / 私聊 thread 回复 Agent 输出 |
| Agent 完成但没有输出 | Bot 回复 `Runtime completed this run with no output.` |
| Agent 执行失败 | Bot 回复一条用户可见失败提示；详细错误仍在 Parsar Run detail / lifecycle event 中排查 |

若 Parsar 里能看到 run 已经 completed / failed，但飞书没有回复：

1. 确认 `PARSAR_FEISHU_OUTBOUND=true`，并检查 server 日志里的 `inflight` 发送 / 重试记录。
2. 查对应 conversation 是否是飞书来源：`conversations.platform='feishu'` 且 `external_id` 非空。
3. 查 run 产出的 agent message metadata 是否包含 `run_id`，且没有 `gateway_delivered_at`。
4. 若 message 已有 `gateway_retry_next_at`，等待退避窗口或查看上一条飞书 API 错误。
5. 若 message 已有 `gateway_delivery_status='dead'`，说明超过重试上限，需要按日志里的飞书错误修配置后重新触发。

## 5. 加固：填 Bot Open ID（推荐）

第一次跑通后回到 §3 的 Agent Connector tab，回填 **Bot Open ID**：

1. 飞书后台 → 应用 → 「凭证与基础信息」页，找 **Bot 的 open_id**（形如 `ou_xxxxxxxx`）
2. 填进 Parsar Agent 详情页的 Bot Open ID 字段 → 保存

**为什么填**：避免 Bot 把自己刚发的消息当成新的入站事件（消息环路自吃）。
不填也能跑，但极端情况会环路放大流量。

---

## 6. 多 Agent：默认 Bot 与独立 Bot

### 6.1 默认 Bot：团队入口

默认 Bot 是团队入口，不需要在当前 Agent 上单独填写飞书应用。没有绑定独立 Bot 的 Agent 可以留在默认 Bot 的可选列表里，由默认入口统一承接。

### 6.2 独立 Bot：每个 Agent 一个 Bot

如果某个 Agent 需要单独的飞书应用、单独进群或单独做权限治理，选择「独立 Bot」，然后重复 §1 → §3 流程。保存后，这个 Agent 会从默认 Bot 的可选列表中移除，入站和出站都走它自己的 Bot 凭证。

**反例（不要这么干）**：

- ❌ 两个启用的独立 Bot Agent 共用同一个 Bot App ID → 409 `feishu_app_id_in_use`，硬阻止
- ❌ 在 OAuth 平台应用上加机器人能力 → 凭证耦合，未来 OAuth 凭证泄露会牵连所有 Bot

---

## 7. 安全注意事项

- App Secret / Verification Token / Encrypt Key **永远不进 repo**，全部走 Secret vault
- 任何文档 / `.env.example` 模板里只放 `<placeholder>`，真值仅在部署 env 或 vault
- `bot_open_id` 不是密文，可以放 jsonb config（事实上就在 `agents.config.connectors.feishu.bot_open_id`）
- 改 visibility 是高敏感操作（特别是切到 `public`），仅 workspace owner/admin 可操作，会写 audit `agent.visibility.changed`
- 改 Bot 绑定本身也写 audit `agent.feishu_connector.updated`，含 old/new app_id 但**不含 *_ref 值**

---

## 8. 已知限制（follow-up）

| 项 | 现状 | 跟进 |
|---|---|---|
| Bot 凭证轮换后出站 worker token 缓存 | 最长 ≤7200s token TTL 内继续用旧 token | 跟进让 PATCH 主动 invalidate worker token cache |
| 同 app_id 并发 PATCH 到两个 Agent 的 TOCTOU 窗口 | 应用层 probe + row lock 但无 UNIQUE 索引兜底 | 跟进加 `UNIQUE PARTIAL INDEX WHERE enabled=true` |
| Feishu Secret kind 仍是自由文本 | UI 自动创建时写入 `feishu_*` kind，但 server 暂不做 enum 校验 | 跟进 server 端注册 `feishu_*` kind enum |
| 群即 conversation 语义 | 独立 Bot 模式仍按现有 conversation 续接规则 | 继续观察真实客户使用 |
| OSS lazy mode（OAuth app = Bot app） | 未实现 | 跟进 env flag 允许复用 |

---

## 9. 相关文档

- `docs/feishu-routing.md` — 飞书 IM 路由 + Agent visibility 设计 source-of-truth
- `docs/deploy/feishu-prod.md` — OAuth 平台应用（用户登录）的生产配置
- `docs/deploy/deploy-runbook.md` — 部署冷启动顺序
- `docs/deploy/health-and-smoke.md` — 健康检查 + smoke 脚本
