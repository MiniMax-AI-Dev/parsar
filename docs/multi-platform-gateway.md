# Parsar 多平台 IM 网关

> 目标:把当前飞书耦合的 IM 网关(`server/internal/gateway/`)抽象成平台中立
> 的 `Channel` 接口,接入 Slack / Discord / 企业微信 / 钉钉。决策日期 2026-06-25。

本文档是 PR #0 —— **接口契约**,代码动工前的最后一道闸。
任何对 `gateway/` 的重构,改动前先读本文档;改动后更新本文档。

---

## 1. 背景与现状

Parsar 当前只接了飞书,代码在两处:

- `server/internal/auth/feishu/` —— OAuth 登录(身份提供方,跟本文档无关)。
- `server/internal/gateway/` —— **IM 消息网关**(飞书 Bot ↔ Agent 收发消息)。

通用契约已经留了一半:`gateway/contract.go` 的 `InboundMessage` / `Delivery` /
`MessageRef` / `ActorRef` / `ConversationRef` 都带 `Gateway` 字段;`gateway_sessions`
表用 `(platform, external_id, external_thread_id)` 复合唯一键;
`conversations.source_gateway` 是 enum 预留。

但生产路径仍深度绑死飞书:

- 类型层:`FeishuInboundEvent` / `FeishuRouteAgent` / `FeishuInboundDecision`,
  `router.go` 全程用飞书专属类型。
- store 层:`GetAgentByFeishuAppID` / `FindUserIDByFeishuUnionID` /
  `ListFeishuSharedBotAgents` —— 方法名写死 Feishu。
- 出站:飞书互动卡片 / reaction / AES webhook 解密 / `app_access_token`
  缓存模型,全部 inlined 在 `feishu*` 包里。

**不改的话,每加一个平台就复制一份飞书代码。** 这是本文档的动机。

---

## 2. 设计原则

### 2.1 复用已经中立的部分

- `connector/` 整套(`PromptInput` / `PromptEvent` / `Capabilities` 等)
  完全不知道飞书存在 —— 不动。
- `internal/agentdaemon/proto/`(server↔daemon WebSocket 协议)跟 IM 平台
  无关 —— 不动。
- `gateway/contract.go` 的 `InboundMessage` / `Delivery` 是基础原语,
  已经被 `devgateway` 验证可用 —— 不动,作为子结构嵌进新接口。
- 路由 / 可见性 / 多 Agent 选择态的逻辑
  (`feishushared/router.go::HandleInbound` 主体) —— **只改命名,不改逻辑**。

### 2.2 编译期 adapter,不做运行时插件

Go 的 `plugin` 包脆弱(版本敏感、dlopen 跨平台问题、加载时机不灵活),
不采用。**每个平台一个 Go 子包**,跟主仓库一起发版。新平台要 PR 进主仓库,
质量门高、抽象纪律好。

参考:Hermes-agent 的 `BasePlatformAdapter` 模式(28+ 平台都这么写)。

### 2.3 能力声明用 flag 集,不用类型层级

每个 adapter 声明 `Capabilities`(12 个 bool / 字段)。driver 读 flag 决定
走哪条路径,不靠 type assert / type switch 选行为。新平台 = 新文件,不改
老代码。

参考:OpenClaw 的 `ChannelCapabilities`(JS 实现,但思想直接借用)。

### 2.4 入站归一化,出站本地渲染

入站侧:每个平台的 raw event → `gateway.InboundEvent`(平台中立)。
出站侧:driver 把"渲染中卡片" / "终态卡片" 概念交给 platform adapter 渲染
成平台原生格式(Block Kit / Embed / 互动卡 / ActionCard),driver 不懂
卡片,只懂"渲染 + 发送 + 编辑"。

**不做"统一卡片 IR"** —— 四个平台的卡片语义不一样,统一了反而难用。

### 2.5 不重写飞书,只搬位置

PR #1 把现有飞书代码原样迁入 `channel/feishu/`,实现新接口,行为零变化。
后续 PR 才逐步泛化。先稳后改,避免大爆炸。

---

## 3. 目标目录结构

```text
server/internal/gateway/
├── contract.go                    # 不动(已是中立)
├── channel/                       # 新增:平台适配层
│   ├── channel.go                 # Channel interface / Capabilities / StreamMode
│   ├── directory.go               # DirectoryAdapter interface
│   ├── lifecycle.go               # Lifecycle interface
│   ├── textcodec.go               # TextCodec interface
│   ├── feishu/                    # 现状迁入,行为零变化(PR #1)
│   │   ├── channel.go             # 实现 Channel + 可选子接口
│   │   ├── verify.go              # ← auth/feishu/webhook.go 迁过来
│   │   ├── event.go               # ← gateway/feishu.go + feishu_message_parser.go
│   │   ├── cards.go               # ← feishu_cards.go + inflight/*cards
│   │   ├── credentials.go         # ← feishu_client.go + outbound_credentials.go
│   │   └── stream.go              # 飞书原地 PATCH 互动卡
│   ├── slack/                      # 首个非飞书平台(PR #4,Socket Mode,按钮-only)
│   │   ├── channel.go              # 身份/Capabilities/Credentials;入站/传输/动作 stub(4a)
│   │   ├── credentials.go          # bot token(xoxb)直用,不走 token 缓存
│   │   ├── blockkit.go             # RenderProgress/RenderTerminal → Block Kit(4a,纯渲染)
│   │   ├── textcodec.go            # mrkdwn 转换 + 长度感知切分(保留代码块边界)
│   │   ├── outbound.go             # chat.postMessage/chat.update(PR #4b)
│   │   ├── verify.go               # Verify:签名密钥 HMAC + url_verification(4c,纯函数)
│   │   ├── event.go                # Normalize:app_mention/message → InboundEvent(4c,纯函数)
│   │   ├── action.go               # HandleAction:block_actions(按钮-only)→ CardAction(4c,纯函数)
│   │   └── socketmode.go           # Socket Mode 实时 runner(驱动上面三个解码器,PR #4d 接线)
│   ├── discord/ (PR #5)
│   ├── wecom/   (PR #6,StreamTerminalOnly)
│   └── dingtalk/(PR #6,StreamTerminalOnly)
├── router/                        # 重命名自 feishushared(PR #2)
│   ├── router.go                  # 改用 channel.Event;channel.Platform() 替 "feishu"
│   ├── commands.go                # /list /select /cancel /help,只去 Feishu 字面量
│   ├── visibility.go              # 不动
│   └── session.go                 # 不动
├── inflight/                      # 重命名自 feishuoutbound(PR #3)
│   ├── driver.go                  # 抢 inflight → 回放 → channel.RenderProgress
│   └── retry.go                   # 1s→5s→30s→5m 退避表保留
└── inbound/                       # 重命名自 feishuinbound(PR #3)
    └── manager.go                 # 改用 channel.Verify + channel.Normalize
```

---

## 4. 接口契约

### 4.1 `Channel` 主接口

```go
package channel

type Platform string

const (
    PlatformFeishu   Platform = "feishu"
    PlatformSlack    Platform = "slack"
    PlatformDiscord  Platform = "discord"
    PlatformWeCom    Platform = "wecom"
    PlatformDingTalk Platform = "dingtalk"
)

type Channel interface {
    Platform() Platform
    Capabilities() Capabilities

    // ① 鉴权 + URL challenge(对接平台 webhook 入口)
    Verify(r *http.Request, body []byte) (verified []byte, challenge string, err error)

    // ② 把平台原始 event 归一化为平台中立的 InboundEvent
    Normalize(verified []byte) (gateway.InboundEvent, error)

    // ③ 命令回执(纯文本 / 简单卡片,不走流式)
    Reply(ctx context.Context, target ReplyTarget, text string) error

    // ④ 出站 driver 用:渲染执行中 / Done / Error 卡片
    RenderProgress(ctx context.Context, target ReplyTarget, state ProgressState) (Card, error)
    RenderTerminal(ctx context.Context, target ReplyTarget, result TerminalResult) (Card, error)

    // ⑤ 流式 PATCH 同一张卡(Edit + BlockStreaming 能力才用)
    Stream() StreamMode
    Edit(ctx context.Context, target ReplyTarget, ref MessageRef, card Card) error
    Send(ctx context.Context, target ReplyTarget, card Card) (MessageRef, error)

    // ⑥ 消息操作(卡片按钮 / Action 回调)
    HandleAction(ctx context.Context, payload []byte) (ActionResult, error)

    // ⑦ Agent prompt 提示(告诉 Agent 在哪个平台上,影响输出格式)
    AgentPromptHint() string

    // ⑧ 凭证解析(hot-reload friendly)
    Credentials() CredentialResolver
}
```

#### `ProgressState` / `TerminalResult`(PR #3a 富化)

> **修订(PR #3a.1):** 初版 `ProgressState`/`TerminalResult` 只有
> `{Title, Body, Done}`,过瘦——飞书执行中/终态卡片携带的是富状态(工具步骤、
> 流式文本、thinking、usage、错误信息)。瘦结构会丢内容 = 行为变化。故 driver
> 把已折叠好的运行状态交给适配器渲染,中立结构纳入 `gateway.StepInfo` /
> `gateway.UsageStats`(`channel` 已 import `gateway`,无环)。Slack/Discord 把同一份
> 中立状态渲染成各自原生 UI。

```go
type ProgressState struct {
    Title         string             // 卡片标题(通常是 agent 名)
    Steps         []gateway.StepInfo // 折叠后的工具调用步骤
    StreamingText string             // 目前为止的流式文本
    Elapsed       time.Duration      // 已运行时长
    Now           time.Time          // 渲染时钟(为零时适配器取 time.Now().UTC())
    Done          bool
}

type TerminalResult struct {
    Title         string
    StreamingText string
    Steps         []gateway.StepInfo
    Thinking      string
    Elapsed       time.Duration
    Usage         *gateway.UsageStats // 无 usage rollup 时为 nil
    Success       bool                // 选 Done vs Error 渲染
    // 错误路径(Success == false 才读)
    ErrorMessage  string // 用户可见失败文案;为空时适配器套默认
    RawError      string // 未映射的原始错误,附在映射文案下
    RunDetailURL  string // run 详情页深链;为空则不渲染
    GuestHint     string // 未注册 public 访客的注册引导
}
```

适配器的 `RenderProgress`/`RenderTerminal` 是纯函数,委托现有
`gateway.BuildRunningCard`/`BuildDoneCard`/`BuildFeishuErrorCardContent`,
golden 测试锁定输出与现产线 `buildMidRunCardContent`/`buildFinalCardForRun`
逐字节一致。

### 4.2 `Capabilities` 声明(吸收 OpenClaw 12 flags)

```go
type Capabilities struct {
    ChatTypes      []string // "dm" | "group" | "channel" | "thread"
    Polls          bool
    Reactions      bool     // emoji reaction;typing indicator 也走这条
    Edit           bool     // 是否能 PATCH 消息本体
    BlockStreaming bool     // 块级流式 PATCH(打字机效果)
    Unsend         bool
    Reply          bool
    Threads        bool     // 平台原生 thread
    Media          bool     // 原生图片/视频/文件
    NativeCommands bool     // 平台原生 slash command
    MaxMessageLen  int      // 单条消息字符上限
    // 不支持的能力直接 false,driver 自动降级
}

type StreamMode int
const (
    StreamPatches      StreamMode = iota // 飞书/Slack/Discord
    StreamTerminalOnly                   // 企业微信/钉钉
)

func (c Capabilities) DerivedStream() StreamMode {
    if c.Edit && c.BlockStreaming {
        return StreamPatches
    }
    return StreamTerminalOnly
}
```

### 4.3 可选子接口(隐式实现,Go duck-typing)

```go
// 目录(列 bot / 群 / 成员);企微/钉钉可能只实现 ListBots
type DirectoryAdapter interface {
    ListBots(ctx context.Context) ([]BotAccount, error)
    ListGroups(ctx context.Context, botID string) ([]Group, error)
    ListMembers(ctx context.Context, botID, groupID string) ([]Member, error)
}

// 文本格式化 + 截断(每个平台字符上限 + 标记语言不一样)
type TextCodec interface {
    Format(text string) string                          // "**粗体**" → Slack "*粗体*"
    Truncate(text string) []string                      // 长度感知切分,代码块边界保留
    ExtractMedia(text string) ([]Media, string)         // 抽图片/文件,留下纯文本
}

// 生命周期 + 健康状态
type Lifecycle interface {
    OnConnect() error
    OnDisconnect()
    OnFatalError(code string, err error, retryable bool) // driver 收到后停 + audit
}
```

driver 用 type assert 探测:

```go
if dc, ok := ch.(DirectoryAdapter); ok { ... } else { /* 降级 */ }
```

### 4.4 平台中立原语(在 `gateway.InboundEvent` 复用 contract.go)

```go
type InboundEvent struct {
    Platform          Platform
    BotID             string                  // 入站事件的 app_id / bot_user_id
    ExternalMessageID string
    ExternalChatID    string
    ExternalThreadID  string
    Sender            ExternalIdentity        // 平台侧 user id
    Text              string
    Attachments       []Attachment
    Raw               json.RawMessage
    ReplyTo           string
}

type ExternalIdentity struct {
    PlatformUserID string
    TenantKey      string                  // tenant_key / team_id / guild_id / corp_id
    DisplayName    string
}
```

---

## 5. 平台能力差异(写进各自的 adapter,不是 driver)

| 能力 | 飞书 | Slack | Discord | 企业微信 | 钉钉 | Teams |
|---|---|---|---|---|---|---|
| 入站鉴权 | verify token + AES-CBC | Signing Secret HMAC + url_verification | Ed25519 / Gateway WS | WXBizMsgCrypt AES | HMAC / Stream 模式 | Bot Framework JWT(RS256/JWKS) |
| 入站接法 | webhook 或 WS | Events API / Socket Mode | Interactions / Gateway | 回调 URL | HTTP 回调 / Stream | Bot Framework webhook(HTTPS POST) |
| 用户 ID 维度 | union_id / open_id | user(Team 维度) | user(global) | userid(corp 维度) | userid / unionid | aadObjectId / 29:(tenant 维度) |
| **流式 PATCH** | ✅ 互动卡 | ✅ chat.update | ✅ message edit | ⚠️ 客服消息可编辑 | ⚠️ ActionCard | ⚠️ PUT activity(整卡,非分块) |
| 卡片格式 | 互动卡 | Block Kit | Embeds + components | 模板卡 / markdown | ActionCard | Adaptive Card |
| 字符上限 | 30 KB(卡片) | 40 000(blocks) | 2 000(embed)/4 000 总 | 2 048 字节(markdown) | 6 000 字符 | 28 KB(卡片) |
| 凭证模型 | app_access_token | bot token(常驻) | bot token(常驻) | corpid+secret→token | appkey+secret→token | AAD client-credentials(app id+password→token) |
| 线程 | root_id / parent_id | thread_ts | 不支持 | 不支持 | 不支持 | conversation.id(root activity) |

**关键决策:Slack / Discord / 飞书都能流式 PATCH,直接用 `StreamPatches`;
企微 / 钉钉不支持,降级为 `StreamTerminalOnly`(只发终态卡,中间不发
"执行中")。**

**Teams 特有:出入站鉴权不对称——入站是 Bot Framework 签发的 JWT(RS256,
issuer `https://api.botframework.com`,audience==MicrosoftAppId,走 JWKS 验签),
出站是 AAD client-credentials 换取的 bearer(scope
`https://api.botframework.com/.default`)。两条鉴权链路职责不同,刻意拆到
`verify.go`(入站验签)与 `outbound.go` 的 `connectorSender`(出站取 token),
不共用一个 credential 类型。群消息必须 @机器人才路由,收敛在
`gateway.ShouldSkipGroupWithoutMention`。**

---

## 6. PR 序列(7 步,每步独立可审,零阻塞飞书生产)

| # | 改动 | 风险 | 价值 |
|---|---|---|---|
| **0** | **本文档** | 无 | **锁定接口** |
| 1 | 引入 `gateway/channel/` 包 + `Channel` 接口;**飞书代码原样搬入** `channel/feishu/`;老调用点继续走旧代码 | 低(纯重构) | 多平台骨架 |
| 2 | `router.go` 去 `FeishuInboundEvent`,改用 `channel.InboundEvent`;store 三个 Feishu 方法加 `platform` 形参 + GIN 索引 | 中 | router/store 中立化 |
| 3 | `inflight driver` 接受 `Channel`;新增 `StreamMode` 字段;`StreamTerminalOnly` 路径 | 中 | 出站框架复用 |
| 4 | **新建 `channel/slack/`(首个非飞书平台)** | 新功能 | 验证抽象 |
| 5 | `channel/discord/` | 新功能 | 验证 WS inbound 路径 |
| 6 | `channel/wecom/` + `channel/dingtalk/`(均 `StreamTerminalOnly`) | 新功能 | 国内场景补齐 |
| 7 | 删除 `auth/feishu/webhook.go` 旧路径,统一走 `channel/feishu/verify.go` | 清理 | 收尾 |

---

## 7. 与参考项目的对齐(已对照过)

| 维度 | Hermes-agent | OpenClaw | 本文档(Parsar) |
|---|---|---|---|
| Adapter 形态 | 抽象基类(继承) | 插件(运行时) | 子包 + 接口(Go duck-typing) |
| 能力声明 | `REQUIRES_EDIT_FINALIZE` bool | `ChannelCapabilities` 12 flags | **`Capabilities` 12 flags(吸收 OpenClaw)** |
| 事件归一 | ❌(各平台 raw) | `ChannelMessagingAdapter` | ✅ `gateway.InboundEvent` |
| 文本截断 | ✅ `truncate_message` | ❌ | ✅ `TextCodec` |
| Agent 平台提示 | `PLATFORM_HINTS` | `agentPrompt` | ✅ `AgentPromptHint()` |
| 目录 | `channel_directory.py` | `ChannelDirectoryAdapter` | ✅ `DirectoryAdapter` |
| 卡片按钮 | ❌ | `ChannelMessageActionAdapter` | ✅ `HandleAction()` |
| 健康/lifecycle | `_mark_connected` | `ChannelLifecycleAdapter` | ✅ `Lifecycle` |
| 配 CLI / 注册 | setup wizard | `ChannelSetupAdapter` | (暂不抽象,跟各平台自助) |
| ID 重打码 | `agent/redact.py` | `ChannelSecurityAdapter` | (暂不抽象,各家差异大) |

**与 Hermes 对齐 ~90%,与 OpenClaw 对齐 ~85%。** 未对齐的两项是有意
不为:OpenClaw 的 `SetupAdapter` 适合 JS CLI,Go 这边各平台 registration
流程差异大不强求统一;`redact` 同理。

---

## 8. 不在本文档范围

- OAuth 登录平台化(那是 `auth/` 目录的事,跟 IM 网关是两条独立链路)。
- `parsar-daemon` ↔ server 的 WebSocket 协议(已是平台中立)。
- Agent Connector 抽象(`connector/types.go`,已中立)。
- 群 → workspace 显式绑定(产品决策,见 `docs/feishu-routing.md` §8)。
- cron / send_message 跨平台路由(Hermes 有,Parsar 暂无此功能)。

---

## 9. 相关文档

- 飞书 IM 路由产品语义:`docs/feishu-routing.md`
- 飞书 inflight driver 重试 / 死信:`docs/feishu-driver-retry.md`
- Agent 执行接入路径与协议:`docs/architecture.md`
- 工程规则(worktree / 强制检查):`AGENTS.md`
