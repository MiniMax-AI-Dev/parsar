# Parsar multi-platform IM gateway

> Goal: extract the currently Feishu-coupled IM gateway
> (`server/internal/gateway/`) into a platform-neutral `Channel` interface
> and wire in Slack / Discord / WeCom / DingTalk. Decision date 2026-06-25.

This document is PR #0 — **the interface contract**, the last checkpoint
before code lands. Any refactor of `gateway/` must read this document
first and update it afterwards.

---

## 1. Background and status

Parsar currently only integrates Feishu; the code lives in two places:

- `server/internal/auth/feishu/` — OAuth login (identity provider; unrelated to this document).
- `server/internal/gateway/` — **IM message gateway** (Feishu Bot ↔ Agent send/receive).

Half of a generic contract is already there:
`gateway/contract.go`'s `InboundMessage` / `Delivery` / `MessageRef` /
`ActorRef` / `ConversationRef` all carry a `Gateway` field; the
`gateway_sessions` table uses a composite unique key
`(platform, external_id, external_thread_id)`; `conversations.source_gateway`
is a reserved enum.

But the production path is still deeply Feishu-bound:

- Type layer: `FeishuInboundEvent` / `FeishuRouteAgent` / `FeishuInboundDecision`; `router.go` uses Feishu-specific types throughout.
- Store layer: `GetAgentByFeishuAppID` / `FindUserIDByFeishuUnionID` / `ListFeishuSharedBotAgents` — the method names hard-code Feishu.
- Outbound: Feishu interactive cards / reactions / AES webhook decryption / the `app_access_token` cache model — all inlined in `feishu*` packages.

**Without a change, every new platform means copying Feishu code.** That
is the motivation for this doc.

---

## 2. Design principles

### 2.1 Reuse what is already neutral

- The entire `connector/` package (`PromptInput` / `PromptEvent` / `Capabilities` and friends) is completely unaware of Feishu — leave it alone.
- `internal/agentdaemon/proto/` (the server↔daemon WebSocket protocol) is IM-platform-agnostic — leave it alone.
- The `InboundMessage` / `Delivery` primitives in `gateway/contract.go` are already proven by `devgateway` — leave them alone and embed them as substructures inside the new interfaces.
- Routing / visibility / multi-Agent selection state logic (`feishushared/router.go::HandleInbound` body) — **rename only; do not touch the logic**.

### 2.2 Compile-time adapters, not runtime plugins

Go's `plugin` package is fragile (version-sensitive, cross-platform dlopen
issues, inflexible loading). Not adopted. **One Go subpackage per
platform**, released together with the main repo. New platforms must PR
into the main repo — high quality gate, tight abstraction discipline.

Reference: Hermes-agent's `BasePlatformAdapter` pattern (28+ platforms use it).

### 2.3 Capability flag sets, not type hierarchies

Each adapter declares `Capabilities` (12 bools / fields). The driver reads
the flags to decide which path to take — not `type assert` / `type switch`
selection. New platforms = new file; no old code changes.

Reference: OpenClaw's `ChannelCapabilities` (JS implementation, borrowed idea).

### 2.4 Normalize inbound, render locally on outbound

Inbound: each platform's raw event → `gateway.InboundEvent`
(platform-neutral). Outbound: the driver hands off "in-progress card" /
"terminal card" concepts to the platform adapter, which renders to the
platform's native format (Block Kit / Embed / interactive card /
ActionCard); the driver knows nothing about cards, only "render + send +
edit".

**No "unified card IR"** — the four platforms have different card
semantics; unifying makes it worse.

### 2.5 Do not rewrite Feishu, only move it

PR #1 moves the existing Feishu code into `channel/feishu/` unchanged and
implements the new interface with zero behavior change. Later PRs
generalize gradually. Stable first, then evolve — avoid a big bang.

---

## 3. Target directory structure

```text
server/internal/gateway/
├── contract.go                    # unchanged (already neutral)
├── channel/                       # new: platform adaptation layer
│   ├── channel.go                 # Channel interface / Capabilities / StreamMode
│   ├── directory.go               # DirectoryAdapter interface
│   ├── lifecycle.go               # Lifecycle interface
│   ├── textcodec.go               # TextCodec interface
│   ├── feishu/                    # current code moved in unchanged (PR #1)
│   │   ├── channel.go             # implements Channel + optional sub-interfaces
│   │   ├── verify.go              # ← moved from auth/feishu/webhook.go
│   │   ├── event.go               # ← from gateway/feishu.go + feishu_message_parser.go
│   │   ├── cards.go               # ← from feishu_cards.go + inflight/*cards
│   │   ├── credentials.go         # ← from feishu_client.go + outbound_credentials.go
│   │   └── stream.go              # in-place PATCH of Feishu interactive cards
│   ├── slack/                      # first non-Feishu platform (PR #4, Socket Mode, buttons-only)
│   │   ├── channel.go              # identity / Capabilities / Credentials; inbound / transport / action stubs (4a)
│   │   ├── credentials.go          # bot token (xoxb) used as-is, no token cache
│   │   ├── blockkit.go             # RenderProgress/RenderTerminal → Block Kit (4a, pure rendering)
│   │   ├── textcodec.go            # mrkdwn conversion + length-aware chunking (preserves code-block boundaries)
│   │   ├── outbound.go             # chat.postMessage / chat.update (PR #4b)
│   │   ├── verify.go               # Verify: signing-secret HMAC + url_verification (4c, pure functions)
│   │   ├── event.go                # Normalize: app_mention/message → InboundEvent (4c, pure functions)
│   │   ├── action.go               # HandleAction: block_actions (buttons only) → CardAction (4c, pure functions)
│   │   └── socketmode.go           # Socket Mode realtime runner (drives the three decoders above; wired up in PR #4d)
│   ├── discord/ (PR #5)
│   ├── wecom/   (PR #6, StreamTerminalOnly)
│   └── dingtalk/(PR #6, StreamTerminalOnly)
├── router/                        # renamed from feishushared (PR #2)
│   ├── router.go                  # switch to channel.Event; channel.Platform() replaces "feishu"
│   ├── commands.go                # /list /select /cancel /help; strip Feishu literals only
│   ├── visibility.go              # unchanged
│   └── session.go                 # unchanged
├── inflight/                      # renamed from feishuoutbound (PR #3)
│   ├── driver.go                  # claim inflight → replay → channel.RenderProgress
│   └── retry.go                   # keep the 1s→5s→30s→5m backoff table
└── inbound/                       # renamed from feishuinbound (PR #3)
    └── manager.go                 # switch to channel.Verify + channel.Normalize
```

---

## 4. Interface contract

### 4.1 `Channel` main interface

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

    // 1) Auth + URL challenge (dock into the platform's webhook entry)
    Verify(r *http.Request, body []byte) (verified []byte, challenge string, err error)

    // 2) Normalize the platform's raw event into a platform-neutral InboundEvent
    Normalize(verified []byte) (gateway.InboundEvent, error)

    // 3) Command reply (plain text / simple card; not streaming)
    Reply(ctx context.Context, target ReplyTarget, text string) error

    // 4) Outbound driver: render running / Done / Error cards
    RenderProgress(ctx context.Context, target ReplyTarget, state ProgressState) (Card, error)
    RenderTerminal(ctx context.Context, target ReplyTarget, result TerminalResult) (Card, error)

    // 5) Streaming PATCH of the same card (only used when Edit + BlockStreaming capabilities are on)
    Stream() StreamMode
    Edit(ctx context.Context, target ReplyTarget, ref MessageRef, card Card) error
    Send(ctx context.Context, target ReplyTarget, card Card) (MessageRef, error)

    // 6) Message actions (card button / Action callbacks)
    HandleAction(ctx context.Context, payload []byte) (ActionResult, error)

    // 7) Agent prompt hint (tell the Agent which platform it is on; affects output format)
    AgentPromptHint() string

    // 8) Credentials resolver (hot-reload friendly)
    Credentials() CredentialResolver
}
```

#### `ProgressState` / `TerminalResult` (enriched in PR #3a)

> **Revision (PR #3a.1):** the initial `ProgressState`/`TerminalResult`
> only had `{Title, Body, Done}`, which was too thin — Feishu's running /
> terminal cards carry rich state (tool steps, streaming text, thinking,
> usage, error info). A thin structure drops content = behavior change.
> So the driver hands the adapter the already-folded running state, and
> the neutral structure lives in `gateway.StepInfo` /
> `gateway.UsageStats` (`channel` already imports `gateway`; no cycle).
> Slack/Discord render the same neutral state into their own native UI.

```go
type ProgressState struct {
    Title         string             // card title (usually the agent name)
    Steps         []gateway.StepInfo // folded tool-call steps
    StreamingText string             // streaming text so far
    Elapsed       time.Duration      // elapsed time
    Now           time.Time          // render clock (zero → adapter uses time.Now().UTC())
    Done          bool
}

type TerminalResult struct {
    Title         string
    StreamingText string
    Steps         []gateway.StepInfo
    Thinking      string
    Elapsed       time.Duration
    Usage         *gateway.UsageStats // nil when there is no usage rollup
    Success       bool                // picks Done vs Error render
    // Error path (only read when Success == false)
    ErrorMessage  string // user-visible failure copy; empty → adapter default
    RawError      string // the unmapped raw error, appended below the mapped copy
    RunDetailURL  string // deep link to the run detail page; empty = do not render
    GuestHint     string // registration guidance for unregistered public visitors
}
```

The adapter's `RenderProgress` / `RenderTerminal` are pure functions,
delegating to existing
`gateway.BuildRunningCard` / `BuildDoneCard` / `BuildFeishuErrorCardContent`;
golden tests pin the output byte-for-byte to the current production
`buildMidRunCardContent` / `buildFinalCardForRun`.

### 4.2 `Capabilities` declaration (absorbing OpenClaw's 12 flags)

```go
type Capabilities struct {
    ChatTypes      []string // "dm" | "group" | "channel" | "thread"
    Polls          bool
    Reactions      bool     // emoji reactions; typing indicator flows through here
    Edit           bool     // whether the message body can be PATCHed
    BlockStreaming bool     // block-level streaming PATCH (typewriter effect)
    Unsend         bool
    Reply          bool
    Threads        bool     // native platform thread
    Media          bool     // native image / video / file
    NativeCommands bool     // native platform slash command
    MaxMessageLen  int      // per-message character cap
    // Unsupported capabilities are simply false; the driver degrades automatically
}

type StreamMode int
const (
    StreamPatches      StreamMode = iota // Feishu / Slack / Discord
    StreamTerminalOnly                   // WeCom / DingTalk
)

func (c Capabilities) DerivedStream() StreamMode {
    if c.Edit && c.BlockStreaming {
        return StreamPatches
    }
    return StreamTerminalOnly
}
```

### 4.3 Optional sub-interfaces (implicit implementation, Go duck-typing)

```go
// Directory (list bots / groups / members); WeCom / DingTalk may implement only ListBots
type DirectoryAdapter interface {
    ListBots(ctx context.Context) ([]BotAccount, error)
    ListGroups(ctx context.Context, botID string) ([]Group, error)
    ListMembers(ctx context.Context, botID, groupID string) ([]Member, error)
}

// Text formatting + truncation (each platform has its own char cap + markup language)
type TextCodec interface {
    Format(text string) string                          // "**bold**" → Slack "*bold*"
    Truncate(text string) []string                      // length-aware chunking; preserve code-block boundaries
    ExtractMedia(text string) ([]Media, string)         // pull out images / files, leave plain text
}

// Lifecycle + health
type Lifecycle interface {
    OnConnect() error
    OnDisconnect()
    OnFatalError(code string, err error, retryable bool) // driver stops + audits on receipt
}
```

Driver detects these via type assertion:

```go
if dc, ok := ch.(DirectoryAdapter); ok { ... } else { /* degrade */ }
```

### 4.4 Platform-neutral primitives (reuse contract.go inside `gateway.InboundEvent`)

```go
type InboundEvent struct {
    Platform          Platform
    BotID             string                  // inbound event's app_id / bot_user_id
    ExternalMessageID string
    ExternalChatID    string
    ExternalThreadID  string
    Sender            ExternalIdentity        // platform-side user id
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

## 5. Platform capability differences (live inside each adapter, not the driver)

| Capability | Feishu | Slack | Discord | WeCom | DingTalk | Teams |
|---|---|---|---|---|---|---|
| Inbound auth | verify token + AES-CBC | Signing Secret HMAC + url_verification | Ed25519 / Gateway WS | WXBizMsgCrypt AES | HMAC / stream mode | Bot Framework JWT (RS256/JWKS) |
| Inbound plumbing | webhook or WS | Events API / Socket Mode | Interactions / Gateway | callback URL | HTTP callback / stream | Bot Framework webhook (HTTPS POST) |
| User ID dimension | union_id / open_id | user (team-scoped) | user (global) | userid (corp-scoped) | userid / unionid | aadObjectId / 29: (tenant-scoped) |
| **Streaming PATCH** | ✅ interactive card | ✅ chat.update | ✅ message edit | ⚠️ customer-service messages editable | ⚠️ ActionCard | ⚠️ PUT activity (whole card, not chunked) |
| Card format | interactive card | Block Kit | Embeds + components | template card / markdown | ActionCard | Adaptive Card |
| Char cap | 30 KB (card) | 40,000 (blocks) | 2,000 (embed) / 4,000 total | 2,048 bytes (markdown) | 6,000 chars | 28 KB (card) |
| Credential model | app_access_token | bot token (long-lived) | bot token (long-lived) | corpid+secret→token | appkey+secret→token | AAD client-credentials (app id + password → token) |
| Threads | root_id / parent_id | thread_ts | not supported | not supported | not supported | conversation.id (root activity) |

**Key decision: Slack / Discord / Feishu all support streaming PATCH → use
`StreamPatches` directly; WeCom / DingTalk do not → degrade to
`StreamTerminalOnly` (only send the terminal card; no "running"
mid-updates).**

**Teams specifics:** inbound and outbound auth are asymmetric — inbound
is a Bot-Framework-signed JWT (RS256, issuer
`https://api.botframework.com`, audience == MicrosoftAppId, validated
against the JWKS); outbound is an AAD client-credentials bearer (scope
`https://api.botframework.com/.default`). These two auth chains are
disjoint and split intentionally into `verify.go` (inbound) and
`outbound.go`'s `connectorSender` (outbound) rather than sharing a
credential type. Group messages must @-the-bot to route, gated by
`gateway.ShouldSkipGroupWithoutMention`.

---

## 6. PR sequence (7 steps, each independently reviewable, zero blockage to Feishu prod)

| # | Change | Risk | Value |
|---|---|---|---|
| **0** | **This document** | None | **Lock the interface** |
| 1 | Introduce `gateway/channel/` + the `Channel` interface; **move Feishu code as-is** into `channel/feishu/`; old callers keep using the old code | Low (pure refactor) | Multi-platform skeleton |
| 2 | `router.go` drops `FeishuInboundEvent` for `channel.InboundEvent`; the three Feishu store methods take a `platform` param + GIN index | Medium | Neutralize router/store |
| 3 | `inflight driver` accepts `Channel`; add a `StreamMode` field; `StreamTerminalOnly` path | Medium | Outbound framework reuse |
| 4 | **New `channel/slack/`** (first non-Feishu platform) | New feature | Validate the abstraction |
| 5 | `channel/discord/` | New feature | Validate the WS inbound path |
| 6 | `channel/wecom/` + `channel/dingtalk/` (both `StreamTerminalOnly`) | New feature | Round out China-region coverage |
| 7 | Delete `auth/feishu/webhook.go`'s old path; consolidate on `channel/feishu/verify.go` | Cleanup | Wrap-up |

---

## 7. Alignment with reference projects (cross-checked)

| Dimension | Hermes-agent | OpenClaw | This doc (Parsar) |
|---|---|---|---|
| Adapter shape | Abstract base class (inheritance) | Plugin (runtime) | Subpackage + interface (Go duck-typing) |
| Capability declaration | `REQUIRES_EDIT_FINALIZE` bool | `ChannelCapabilities` 12 flags | **`Capabilities` 12 flags (adopted from OpenClaw)** |
| Event normalization | ❌ (raw per platform) | `ChannelMessagingAdapter` | ✅ `gateway.InboundEvent` |
| Text truncation | ✅ `truncate_message` | ❌ | ✅ `TextCodec` |
| Agent platform hint | `PLATFORM_HINTS` | `agentPrompt` | ✅ `AgentPromptHint()` |
| Directory | `channel_directory.py` | `ChannelDirectoryAdapter` | ✅ `DirectoryAdapter` |
| Card buttons | ❌ | `ChannelMessageActionAdapter` | ✅ `HandleAction()` |
| Health/lifecycle | `_mark_connected` | `ChannelLifecycleAdapter` | ✅ `Lifecycle` |
| Config CLI / registration | setup wizard | `ChannelSetupAdapter` | (not abstracted; each platform self-services) |
| ID redaction | `agent/redact.py` | `ChannelSecurityAdapter` | (not abstracted; per-platform differences are large) |

**Alignment with Hermes ~90%, with OpenClaw ~85%.** The two we deliberately
do not adopt: OpenClaw's `SetupAdapter` fits a JS CLI, and Go here has
enough per-platform registration variance that forcing a common shape is
not worth it; same story for `redact`.

---

## 8. Out of scope

- Platform-ifying OAuth login (that lives under `auth/`; a different chain from the IM gateway).
- The `parsar-daemon` ↔ server WebSocket protocol (already platform-neutral).
- Agent Connector abstraction (`connector/types.go`, already neutral).
- Explicit group → workspace binding (product decision; see `docs/feishu-routing.md` §8).
- Cross-platform cron / send_message routing (Hermes has it; Parsar does not have this feature yet).

---

## 9. Related docs

- Feishu IM routing product semantics: `docs/feishu-routing.md`
- Feishu inflight driver retries / dead letters: `docs/feishu-driver-retry.md`
- Agent execution entry paths and protocol: `docs/architecture.md`
- Engineering rules (worktrees / mandatory checks): `AGENTS.md`
