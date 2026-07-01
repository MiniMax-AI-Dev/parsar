# 接入 `pi` 作为新的 agent_kind —— 接入手册

> 目标：把 [`@earendil-works/pi-coding-agent`](https://github.com/earendil-works/pi)（Pi Agent Harness 的交互式 coding agent CLI）接进 Parsar，作为 `parsar-daemon` 下一个新的 **`agent_kind = "pi"`**。
>
> 适用读者：要动手实现 adapter 的工程师 / Agent。先读 [`AGENTS.md`](../../AGENTS.md) 与 [`docs/architecture.md`](../architecture.md)。

---

## 0. 结论先行

| 判断项 | 结论 |
|---|---|
| pi 是什么 | 一个**交互式 coding agent CLI**，和 claude_code / opencode / codex 同类 |
| 接入层级 | **场景 A：新增 `agent_kind`**，复用现有 `agent_daemon` 连接器。**不是**新 `connector_type` |
| 是否能 headless 包装 | **能**。pi 有成熟的 `--mode json`（单发 NDJSON 流）和 `--mode rpc`（常驻）两种无 TUI 模式 |
| MVP 改动范围 | **纯 daemon 侧**：新建一个 adapter 包 + `connect.go` 接 8 处线。**dispatch 路径零 server 改动**（BYO key 模式下） |
| server / web 改动 | **可选**：仅当需要「Parsar 托管模型注入」「能力(skill/MCP)渲染」「Web 后台下拉创建 pi agent」时才改 |
| DB migration | **不需要**。`agent_kind` 存在 `agents.config` 的 JSONB 里，无 CHECK 约束；心跳的 `supported_agent_kinds` 也是 JSONB |

一句话：**pi 的接入是「克隆 opencode adapter → 改 CLI 拼参 → 改 stdout 解析」三件事**，外加 `connect.go` 注册。

> **本次接入范围（已与负责人锁定）**
>
> | 维度 | 决策 |
> |---|---|
> | Daemon adapter（流式正文/思考/工具/用量/续聊） | ✅ 做 |
> | 模型来源 | ✅ **Parsar 托管模型注入**（非 BYO env key） |
> | 托管 skill | ✅ **纳入**（pi 原生 Agent Skills，经 `--skill` 注入，与 claude_code 同 `SKILL.md` 标准） |
> | 托管 mcp / plugin | ❌ pi 不支持 → 标 `ErrUnsupported`（同 opencode/codex，用户见"该能力不支持"提示） |
> | AskUserQuestion 人在环 | ❌ 不做（pi 无此能力，两个 Submit 返回 Unknown 哨兵） |
> | Web 后台创建入口 | ✅ **纳入**（创建向导加第 4 张引擎卡，对齐 codex/claude_code） |
> | DB migration | ❌ 不需要 |
>
> 因此 §4.2「可选」项里的 **托管模型注入 / 托管 skill / Web 后台** 三项本次均**转为必做**；详细排期见文末 **§9 落地 Plan**。

---

## 1. 抽象回顾（你要落在哪一层）

```
Server  ──(connector_type=agent_daemon)──> agent_daemon 连接器
                                              │  WebSocket(proto.Envelope)
                                              ▼
parsar-daemon: dispatch.Router ──(agent_kind)──> agent.Factory ──> agent.Session
                                                                     │
                                            claudecode / opencode / codex / 【新增 pi】
```

你要实现的是下层 `agent.Factory` + `agent.Session`（`apps/parsar-daemon/internal/agent/registry.go:37` / `:41`）。契约：

- **`Factory(ctx, req proto.PromptRequestPayload, out chan<- proto.Envelope) (Session, error)`**——一个 prompt 起一个 session。
- **`Session` 三个方法**：
  - `Cancel(ctx) error`——幂等、best-effort（SIGTERM→SIGKILL）。
  - `SubmitPermission(ctx, permID, decision) error`——pi 无权限系统，**直接返回 `agent.ErrUnknownPermission`**。
  - `SubmitPromptForUserChoice(ctx, askID, decision) error`——pi 无此能力，**直接返回 `agent.ErrUnknownAsk`**。
- **channel 所有权**：adapter 拥有 `out` 的 close 权。**必须**在发完终态 `done` 或 `error` 帧之后 `close(out)` 恰好一次——router 用这个 close 当「会话结束」信号（`registry.go:5-15`）。

---

## 2. pi 侧事实速查（实现 adapter 前必须知道的）

> 全部基于本地克隆 `pi/`（已 gitignore，见仓库根 `.gitignore` 的 `/pi/`）。版本 `0.80.2`。

### 2.1 二进制与运行前提
- 命令名 **`pi`**（`pi/packages/coding-agent/package.json:9-11`，`bin.pi = dist/cli.js`）。
- npm 包 `@earendil-works/pi-coding-agent`；**Node ≥ 22.19.0**（`package.json:97-99`）。
- 安装：`npm i -g @earendil-works/pi-coding-agent`（也支持 pnpm/yarn/bun，及 `bun build --compile` 出的独立二进制）。

### 2.2 headless 单发模式（adapter 用这个）
```bash
pi --mode json -p "<prompt>"
```
- `--mode json` → stdout 输出 **NDJSON**（每行一个 JSON 对象），实时流式（`pi/packages/coding-agent/src/modes/print-mode.ts:104-108`）。
- stdin 非 TTY 时自动进入 print 模式（`main.ts:762-768`）；prompt 可走 argv 或 stdin 管道。
- **无 `--cwd` 标志**：工作目录取 `process.cwd()`（`main.ts:480`）。Parsar 用子进程 `cmd.Dir = req.WorkDir` 设定即可（opencode 已是这套）。

### 2.3 NDJSON 事件流（你要解析的东西）
- 第一行通常是 **session header**：`{"type":"session","version":3,"id":"<uuidv7>","cwd":"..."}`（`print-mode.ts:112-116`）。→ **抓 `id` 用于 resume**。
- 之后是 `AgentEvent`（`pi/packages/agent/src/types.ts:413-428`）：

| 事件 `type` | 含义 | 映射到 proto |
|---|---|---|
| `agent_start` / `turn_start` | 生命周期开始 | 忽略 |
| `message_update` | 流式增量，**带 `assistantMessageEvent`** | 见下 |
| `message_end` | 一条消息结束，`message.usage` 带 token | `proto.TypeUsage` |
| `tool_execution_start` | 工具调用开始 | `proto.TypeToolCall`（stage=`before`，可选） |
| `tool_execution_end` | 工具调用结束（`result`,`isError`） | `proto.TypeToolCall`（stage=`after`，可选） |
| `agent_end` | 全部结束 | 触发 `proto.TypeDone` |

- `message_update.assistantMessageEvent` 的形状（`pi/packages/ai/src/types.ts:453-457`）：
  - `{type:"text_delta", delta:"...", partial:...}` → **增量正文** → 累加 + 发 `proto.TypeDelta`。
  - `text_start` / `text_end` → 边界，可忽略正文（只取 delta）。
  - ⚠️ 这个 union 还有 reasoning/thinking、tool 变体——实现前**完整读一遍 `types.ts:453` 起的 union**，把 thinking 类映射到 `proto.TypeThinking`。

### 2.4 会话续聊（resume）
- pi 默认把 session 持久化为 JSONL：`~/.pi/agent/sessions/--<cwd>--/<ts>_<id>.jsonl`（`session-manager.ts:439-444`）。
- 续聊标志：
  - `--session <id|path>`——按 id/路径打开（**推荐**）。
  - `--session-id <id>`——用指定 id（不存在则创建）。
  - `--continue` / `-c`——续当前 cwd 最近一次。
  - `--no-session`——纯内存、不落盘。
- **Parsar 续聊接法**（对齐 claude_code）：第一轮不带 resume → 从 header 抓 `id` → 在 `done` 帧 `Metadata` 里回带（如 `{"pi_session_id": id}`）→ server 落 `connector_session_bindings.metadata` → 下一轮经 `req.ResumeSessionID` 下发 → adapter 拼 `--session <id>`。
- 能力位 `Resume: true`。

### 2.5 权限 / 审批
- **pi 没有工具级权限系统**，没有 `--yolo`/`--auto-approve`（README「Permissions & Containerization」，`project-trust.ts`）。
- 唯一的「approve」是**项目信任**（是否加载仓库里的 `.pi/` 配置/扩展）：`--approve`/`-a` 信任、`--no-approve`/`-na` 不信任（`args.ts:180-182`）。
- ✅ **headless 不会卡住、不会问用户**：`--mode json` 时 `hasUI=false`，遇到带 `.pi/` 的仓库会**静默判为不信任（false）→ 不加载该 `.pi/`**，绝不弹信任提示等输入（`project-trust.ts:86-88`；`package-manager-cli.ts:535` 设 `hasUI: appMode==="interactive"`）。pi 对内置 `read/bash/edit/write` **从来不做逐次审批**——没有 `--dangerously-skip-permissions`，因为它压根没有要跳过的权限系统。
- **安全默认：仍显式传 `--no-approve`**——不是为了防卡住（headless 本就不会卡），而是把行为钉死：防止某台 runtime 的 `~/.pi/agent/settings.json` 设了 `defaultProjectTrust:"always"` 时被静默加载任意仓库的 `.pi/` 扩展（= 潜在任意代码执行）。需要主动加载项目级 skill/扩展时才用配置项打开 `--approve`（同样无提示）。
- 能力位 `Permissions: false`。Parsar 的 permission_request / AskUserQuestion 拦截路径对 pi **不适用**。

### 2.6 鉴权 / 选模型
- Provider key 走环境变量：`ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GEMINI_API_KEY` / …（`args.ts:335-377`）；或 `--api-key <key>`（需配合 `--model`，`main.ts:701-709`）。
- 选模型：`--provider <name> --model <pattern>`，或合并写法 `--model openai/gpt-4o`、`--model sonnet:high`；默认 provider 是 `google`（`args.ts:238`）。
- 配置文件：`~/.pi/agent/settings.json`（全局）、`<cwd>/.pi/settings.json`（项目，深合并）。
- **MVP（BYO key）**：在 daemon 宿主/沙盒里配好 `*_API_KEY` 环境变量即可，**不碰 server 的托管模型注入**。

### 2.7 其它
- 系统提示：`--system-prompt <text>`（整体替换）/ `--append-system-prompt <text>`（追加、可重复）/ `<cwd>/.pi/SYSTEM.md`。
- 工具开关：`--tools/-t`、`--exclude-tools/-xt`、`--no-tools`、`--no-builtin-tools`；内置 `read/bash/edit/write`（`grep/find/ls` 默认关）。
- 思考档：`--thinking off|minimal|low|medium|high|xhigh`。
- **不支持 MCP**（pi 用自有 extension 体系）。Parsar 的 MCP 能力渲染对 pi 无意义。
- 版本：`pi --version` 打印版本并 exit 0（`main.ts:515-518`）——给 preflight 用。

---

## 3. 能力映射（心跳广播给 server 的 descriptor）

`proto.AgentKindCapabilities`（`internal/agentdaemon/proto/inbound.go:194`）：

```go
proto.SupportedAgentKind{
    Kind: "pi",
    Capabilities: proto.AgentKindCapabilities{
        Streaming:   true,  // --mode json 流式 NDJSON
        Permissions: false, // 无工具级权限系统
        Usage:       true,  // message_end 带 usage（provider 相关，可能不全）
        Resume:      true,  // --session / --continue
    },
}
```
`Available` 由 preflight 探测 `pi --version` 的结果填。

---

## 4. 改动清单（一图看全）

### 4.1 必改（daemon 侧，MVP 充分）

| # | 文件 | 改动 |
|---|---|---|
| 1 | `apps/parsar-daemon/internal/agent/pi/`（新建包） | `session.go` / `options.go` / `parser.go` / `version.go` + 测试，**克隆 opencode** |
| 2 | `apps/parsar-daemon/internal/cli/connect.go` | 8 处接线（见 §6 Step 2） |

### 4.2 可选（按需）

| 触发条件 | 文件 | 改动 |
|---|---|---|
| 要 Parsar **托管模型注入**（config 配 `model_id`） | `server/internal/connector/agentdaemon/model_injection.go:242` | `switch agentKind` 加 `case "pi":` + `injectPiManagedModel(...)`；否则 default 返回 `ErrUnsupportedAgentKind` |
| 要给 pi agent 挂 Parsar **托管 skill/MCP/plugin/system_prompt** | `server/internal/connector/agentdaemon/capability_runtime.go:183` | `agentKindToRenderTarget` 加 `case "pi":`（+ `render.TargetPi`）。**不改也能跑**：未知 kind 落 `TargetClaudeCode` |
| 要在 **Web 后台**下拉里创建 pi agent | `apps/web/src/pages/admin/CreateAgentDialog.tsx:42` 等 | `AgentEngine` union 加 `"pi"`、加一张 ChoiceCard、`requiresModel`/`agentEngineFromAgent` 同步 |

> 为什么 BYO 模式零 server 改动：`injectManagedModel` 在没有 `model_id`/`default_model_id` 时**早返回 nil**（`model_injection.go:121-128`），根本走不到那个 `switch`；dispatch 路径上 `resolveAgentKind` 只从 config 读字符串、`validateAgentKindForSession` 纯靠心跳广播校验，都无硬编码 allowlist。

### 4.3 不用动

- `registry.go` / `dispatch/router.go`——通用，靠 `RegisterKind` + `Resolve(agent_kind)` 驱动。
- `proto/outbound.go` / `proto/inbound.go`——wire schema 已通用。
- DB / migrations——`agent_kind` 在 JSONB，无 CHECK。

---

## 5. 数据流走查（pi 一次 run）

1. 运营在 `agents.config` 里设 `agent_kind="pi"` 并绑定 `runtime_id`（某台跑了 `parsar-daemon` 且 `pi` 可用的设备）。
2. server `agent_daemon` 连接器 `resolveAgentKind` 读出 `"pi"`，对照该设备心跳的 `supported_agent_kinds` 校验 available。
3. 连接器把 `PromptInput` 翻成 `proto.PromptRequestPayload`（`agent_kind="pi"`、`WorkDir`、`AgentOptions`、`ResumeSessionID`），WS 下发。
4. daemon `dispatch.Router.handlePromptRequest` → `registry.Resolve("pi")` → `pi.Factory` 建 session，起 `pump`。
5. session 跑 `pi --mode json -p ...` 子进程，逐行解析 NDJSON → 翻成 `delta/thinking/tool_call/usage/done` 上行帧写 `out`。
6. 终态：发 `done`（`Content` = 累加正文，`Metadata` 带 `pi_session_id`）后 `close(out)` → 连接器落库、回灌飞书/Web。

---

## 6. 落地步骤（Step-by-step）

### Step 0：前置
- ⚠️ **按 `AGENTS.md`：所有代码改动必须在从 `origin/main` 切出的 worktree 里做，禁止直接在 `main` 提交。**
  ```bash
  git fetch origin main
  git worktree add .worktrees/pi-agent-kind -b feature/pi-agent-kind origin/main
  ```
- 在目标 runtime 上装 pi：`npm i -g @earendil-works/pi-coding-agent`，`pi --version` 自检；配好 provider key 环境变量。

### Step 1：新建 adapter 包 `apps/parsar-daemon/internal/agent/pi/`
**直接 `cp` opencode 包改名**，逐文件改：

- **`version.go`**（最简单，先做）——照 `opencode/version.go`：
  - `defaultBinary = "pi"`、`InstallURL = "https://pi.dev/docs/latest"`、`ErrCLINotFound`。
  - `CheckCLIAvailable(ctx, binary) (string, error)`：`LookPath` → 跑 `pi --version` → 返回 trim 后的版本串。

- **`options.go`**——`BuildArgs(runID, prompt, workDir, opts map[string]any) (BuildResult, error)`，拼 pi 的 CLI：
  - 基线 `args := []string{"--mode", "json", "-p"}`，外加 **`--no-approve`**（安全默认；可由 opts 翻成 `--approve`）。
  - 续聊：`req.ResumeSessionID != ""` → `--session <id>`。
  - 读 `opts` 键（沿用 Parsar 既有键名，未知键忽略）：

    | opts 键 | 类型 | → pi 参数 |
    |---|---|---|
    | `model_selector` / `model` | string | `--model <v>`（前者优先；支持 `provider/model`） |
    | `override_system_prompt` | string | `--system-prompt <v>`（整体替换） |
    | `system_prompt` | string | `--append-system-prompt <v>`（追加，spec/memory 注入走这里） |
    | `thinking` | string | `--thinking <v>` |
    | `allowed_tools` / `tools` | []string/csv | `--tools <csv>` |
    | `exclude_tools` | []string/csv | `--exclude-tools <csv>` |
    | `approve_project_trust` | bool | true → `--approve`，false/缺省 → `--no-approve` |
    | `env` | map[string]string | 注入子进程环境（provider key 等） |
    | `api_key` | string | `--api-key <v>`（仅托管模型注入时才有；需同时有 `--model`） |
  - `WorkDir` 解析照搬 opencode：必须绝对路径或 `~/` 开头，不存在则 `MkdirAll`；通过 `cmd.Dir` 传（pi 无 `--cwd`）。
  - prompt 作为 `-p` 的实参（或走 stdin）。

- **`parser.go`**——核心。`translator` 逐行 `json.Unmarshal` 后按 `type` 分发：
  - `"session"` → 记 `header.id`（终态 `done.Metadata["pi_session_id"]` 回带）。
  - `"message_update"` → 取 `assistantMessageEvent`：`text_delta` 累加 `delta` 并发 `proto.TypeDelta`；reasoning/thinking 变体发 `proto.TypeThinking`。
  - `"message_end"` → 读 `message.usage` 发 `proto.TypeUsage`（`Provider="pi"` 或实际 provider）。
  - `"tool_execution_start"`/`"_end"` →（可选）发 `proto.TypeToolCall`（stage before/after）。
  - 非 JSON 行 → 进 plain buffer，兜底（同 opencode：全程无 delta 时把 plain 当一条 delta）。
  - **终态**：stdout EOF 后 `cmd.Wait()`；`waitErr != nil`（且非 cancel）→ 发 `proto.TypeError`（带截断的 stderr）；最后**总是**发 `proto.TypeDone`（`Content` = 累加正文，`Metadata` 带 `pi_session_id` + `connector_path:"pi_run"`）。

- **`session.go`**——照 `opencode/session.go`：
  - `Factory` → `newSession`：`BuildArgs` → `exec.CommandContext(ctx, cfg.piBinary, args...)`，`cmd.Dir = workDir`，`cmd.Env = append(os.Environ(), buildRes.Env...)`，接 stdout/stderr，`Start`，起 `run(stdout)` + `pumpStderr(stderr)` 两个 goroutine。
  - `run()` defer 链保证 `close(waitDone)`→`cleanup()`→`closeOut()`，**守住 close(out) 恰好一次**。
  - `Cancel()`：`SIGTERM`，`killTimeout`（默认 3s）后升 `SIGKILL`。
  - `SubmitPermission` 返回 `agent.ErrUnknownPermission`；`SubmitPromptForUserChoice` 返回 `agent.ErrUnknownAsk`。

- **`export_test.go` + `*_test.go`**——照 opencode：用 `TestMain` 把测试二进制伪装成 fake `pi`（环境变量选 role：`json-success`/`plain`/`nonzero`/`hang`），断言 delta+usage+done 序列、plain 兜底、非零退出 error+done、cancel 关 out、空 prompt 拒绝、nil out 拒绝。

### Step 2：`connect.go` 接 8 处线
照 opencode（`apps/parsar-daemon/internal/cli/connect.go`）：

1. import 加 `piagent ".../internal/agent/pi"`。
2. `agentCLIDiscovery` 结构体加 `Pi proto.SupportedAgentKind`（`:192`）。
3. `agentCLIChecks` 结构体加 `Pi func(context.Context, string) (string, error)`（`:198`）。
4. `defaultAgentCLIChecks` 加 `Pi: piagent.CheckCLIAvailable`（`:204`）。
5. `discoverAgentCLIs` 初始 descriptor 加 `Pi: proto.SupportedAgentKind{Kind:"pi", Capabilities: {Streaming,Usage,Resume:true}}`。
6. `discoverAgentCLIs` 加 pi 版本探测块（仿 opencode `:271-284`）：成功置 `Available/Version`，`ErrCLINotFound` 打安装提示。
7. 「无可用 CLI」兜底判断（`:301`）加 `&& !out.Pi.Available`。
8. `registerAgentKinds`（`:307`）加 `registry.RegisterKind(agentCLIs.Pi, piagent.Factory)`。

> 心跳 `supported_agent_kinds` 由 `registry.SupportedAgentKinds()` 自动带上，无需额外改。

### Step 3（可选）：托管模型注入
若要在 Parsar 里给 pi agent 配 `model_id`（而非 BYO env key）：
- `server/internal/connector/agentdaemon/model_injection.go:242` 的 `switch` 加 `case "pi":`，新增 `injectPiManagedModel(opts, modelID, mr, apiKey)`：把解出的 `apiKey`/`provider`/`model` 写进 `opts`（adapter 再翻成 `--api-key` + `--model`）。

### Step 4（可选）：Web 后台
`apps/web/src/pages/admin/CreateAgentDialog.tsx`：`AgentEngine` 加 `"pi"`、加 ChoiceCard、同步 `requiresModel` / `agentEngineFromAgent`。**不做也行**——可先用 seed/SQL/API 直接写 `project_agents.config.agent_kind="pi"` 来创建/测试。

### Step 5：创建一个 pi agent
- 最快：在某条 `project_agents` 记录的 `config` JSONB 写 `{"agent_kind":"pi", ...}`，`runtime_id` 绑到装了 pi 的设备。
- 该设备上 `parsar-daemon connect`，确认启动日志里 pi `preflight ok (<version>)`、心跳广播含 `pi`。

### Step 6：验证
```bash
make check                      # AGENTS.md 必跑
# 仅 daemon 包：
go test ./apps/parsar-daemon/internal/agent/pi/...
go test ./apps/parsar-daemon/internal/cli/...
```
- 手动冒烟：在装了 pi 的机器上 `pi --mode json -p "say hi"`，确认 NDJSON 形状和你解析的一致。
- 端到端：飞书群里 @ 这个 pi agent 发一句，确认有流式回复 + 落库 + 续聊（第二句带上了 `--session`）。

---

## 7. 关键坑（提交前自查）

1. **`close(out)` 恰好一次**：所有路径（正常结束 / 非零退出 / cancel / factory 后子进程起不来）都要先发终态帧再 close，否则 router 的 pump 永久阻塞。
2. **headless 信任不会卡**：headless（`hasUI=false`）遇到带 `.pi/` 的仓库会**静默判为不信任并跳过加载**，不会弹提示等输入（`project-trust.ts:86-88`），用户**全程无需审批**。仍建议显式 `--no-approve` 把行为钉死（防止全局 `defaultProjectTrust:"always"` 静默加载仓库扩展）；要主动加载项目 skill/扩展才传 `--approve`。**不要**安装 `examples/extensions/permission-gate.ts` 这类扩展——那是唯一会重新引入审批提示的东西。
3. **无 `--cwd`**：靠 `cmd.Dir` 设工作目录；sandbox 模式 `WorkDir` 为空时按 opencode 的 per-conversation scratch dir 兜底。
4. **session 按 cwd 归档**：pi 的 `--continue` 依赖 cwd 稳定；Parsar 续聊请用**显式 `--session <id>`**（从 header 抓的 id），不要只靠 `--continue`。
5. **usage 是 provider 相关的**：`message_end.usage` 可能字段不全或缺失，解析要容错；`Usage` 能力位标 true 但别假设一定有 cost。
6. **不要映射不存在的能力**：pi 没有权限/AskUserQuestion/MCP——`Permissions=false`，两个 Submit 直接返回 Unknown 哨兵，别去拼不存在的 flag。
7. **thinking union 要读全**：`assistantMessageEvent` 不止 text，reasoning/thinking 变体要正确归到 `proto.TypeThinking`，否则会把思考链拼进正文。
8. **Node 版本**：runtime 需 Node ≥ 22.19；preflight `pi --version` 失败时给清楚的安装/升级提示（仿 opencode 的 `InstallURL`）。

---

## 8. 参考文件索引

**Parsar 抽象层**
- `apps/parsar-daemon/internal/agent/registry.go:37/41/86/93/108/132` —— Factory / Session / RegisterKind / Resolve
- `internal/agentdaemon/proto/inbound.go:194/204/215` —— AgentKindCapabilities / SupportedAgentKind / Heartbeat
- `internal/agentdaemon/proto/outbound.go:37` —— PromptRequestPayload

**模板（opencode adapter）**
- `apps/parsar-daemon/internal/agent/opencode/{session,options,parser,version}.go`
- `apps/parsar-daemon/internal/cli/connect.go:192/198/204/216/271-284/301/307`

**可选 server 改动点**
- `server/internal/connector/agentdaemon/model_injection.go:119/121-128/242`
- `server/internal/connector/agentdaemon/capability_runtime.go:183`
- `apps/web/src/pages/admin/CreateAgentDialog.tsx:42`

**pi 上游（本地克隆 `pi/`，已 gitignore）**
- 入口 / 参数：`packages/coding-agent/src/cli.ts:20`、`src/cli/args.ts:63`、`src/main.ts:100-111/480/515-518/762-768`
- headless 输出：`packages/coding-agent/src/modes/print-mode.ts:104-116`
- 事件类型：`packages/agent/src/types.ts:413-428`、`packages/ai/src/types.ts:453-457`
- 会话：`packages/coding-agent/src/core/session-manager.ts:439-444`
- 信任：`packages/coding-agent/src/core/project-trust.ts`
- 鉴权：`packages/coding-agent/src/cli/args.ts:335-377`

---

## 9. 落地 Plan（本次锁定 scope）

> 范围见文首「本次接入范围」框。无 DB migration。所有改动按 `AGENTS.md` 在 worktree 内完成，提交前跑 `make check`。
>
> **依赖顺序**：Phase 1→2 是地基（daemon 能跑、心跳广播 pi）；Phase 3 / 4 / 5 相对独立，可并行。

### Phase 0 — 前置
- `git fetch origin main && git worktree add .worktrees/pi-agent-kind -b feature/pi-agent-kind origin/main`
- 目标 runtime 装 pi：`npm i -g @earendil-works/pi-coding-agent`（Node ≥ 22.19），`pi --version` 自检
- 冒烟：`pi --mode json -p "say hi"`，确认 NDJSON 形状与 §2.3 一致

### Phase 1 — Daemon adapter（核心，纯 daemon，可独立测）⟶ 新建 `apps/parsar-daemon/internal/agent/pi/`
克隆 opencode 包，逐文件改：
1. **`version.go`**：`defaultBinary="pi"`、`InstallURL`、`ErrCLINotFound`、`CheckCLIAvailable`（跑 `pi --version`）。
2. **`options.go`**：`BuildArgs` 基线 `--mode json -p` + `--no-approve`；opts→flags 映射见 §6 Step1 表，额外：
   - 托管模型产物 `model`→`--model <provider/model>`、`api_key`→`--api-key`（pi 要求 `--api-key` 必配 `--model`）
   - 托管 skill 产物 `skills`→逐个 `--skill <解压目录>`（解压见 #3）
   - `WorkDir`→`cmd.Dir`（pi 无 `--cwd`）；resume→`--session <id>`
3. **`skills.go`**（新增，参考 claude daemon 侧解压逻辑）：读 `opts["skills"]` 的 `{name,download_url,sha256}`→下载 zip→校验 sha256→解压到 scratch `<workDir>/.pi-skills/<name>/`→把目录路径回给 options 拼 `--skill`。
4. **`parser.go`**：逐行 NDJSON 分发（见 §2.3）：`session`→记 id；`message_update.assistantMessageEvent` 的 `text_delta`→`TypeDelta`（累加）、reasoning/thinking 变体→`TypeThinking`；`message_end.usage`→`TypeUsage`（cost 容错）；`tool_execution_start/end`→`TypeToolCall`；EOF→`Wait()`，非 cancel 错→`TypeError`，**总是** `TypeDone`（Metadata 带 `pi_session_id`）；非 JSON 行→plain 兜底。
5. **`session.go`**：Factory→newSession→`exec.CommandContext`+`cmd.Dir`+`cmd.Env`→run+pumpStderr；defer 链守 `close(out)` 恰好一次；Cancel SIGTERM→3s→SIGKILL；`SubmitPermission`→`ErrUnknownPermission`、`SubmitPromptForUserChoice`→`ErrUnknownAsk`。
6. **`*_test.go` + `export_test.go`**：fake pi（json-success/plain/nonzero/hang），断言 delta+usage+done、plain 兜底、非零退出 error+done、cancel 关 out、空 prompt 拒绝、nil out 拒绝。
- ✅ `go test ./apps/parsar-daemon/internal/agent/pi/...`

### Phase 2 — connect.go 接线 ⟶ `apps/parsar-daemon/internal/cli/connect.go`
照 opencode 接 8 处（见 §6 Step2）。Capabilities：`Streaming/Usage/Resume=true, Permissions=false`。
- ✅ `go test ./apps/parsar-daemon/internal/cli/...`；`parsar-daemon connect` 日志见 `pi preflight ok (<ver>)`、心跳含 pi

### Phase 3 — 托管模型注入 ⟶ `server/internal/connector/agentdaemon/model_injection.go:242`
`switch agentKind` 加 `case "pi":`→新增 `injectPiManagedModel(opts, modelID, mr, apiKey)`：解 model_id→(provider, model_key, api_key)，写 `opts["model"]="<provider>/<model_key>"` + `opts["api_key"]`；补 Parsar provider 名→pi provider id 映射（anthropic/openai/google/…）。
- ⚠️ 核对：create-agent **后端 API** 是否对 `agent_kind` 有 allowlist 校验（dispatch 路径无；admin 创建路径需确认，有则加 pi）。
- ✅ server 单测 + 端到端确认子进程拿到正确 `--model`/`--api-key`

### Phase 4 — 托管 skill render target ⟶ `server/internal/connector/agentdaemon/{render/*, capability_runtime.go}`
1. `render` 包：加 `TargetPi` 常量 + `piRenderer`：`KindSkill`→复用 claude 的 `{name,version,oss_key,sha256}` descriptor（同 Agent Skills 标准）、`KindMCP`/`KindPlugin`→`ErrUnsupported`、`KindSystemPrompt`→共用 `renderSystemPrompt`；注册进 `render.For()`。
2. `capability_runtime.go:183` `agentKindToRenderTarget` 加 `case "pi": return TargetPi`（消除未知 kind 错落 `TargetClaudeCode`）。
- ⚠️ 首个 skill 实测确认 zip 内是标准 `SKILL.md` 布局（claude 与 pi 同标准，理论可直用）。
- ✅ render 单测；绑 skill 的 pi agent 收到 `--skill` 生效；绑 mcp 见 unsupported 提示

### Phase 5 — Web 创建卡 ⟶ `apps/web/src/pages/admin/CreateAgentDialog.tsx`
`AgentEngine` union 加 `"pi"`（`:42`）、`agentEngineFromAgent` 加 pi（`:60`）、`requiresModel` 含 pi（`:586`）、Step2 加第 4 张 `<ChoiceCard>`（`:994`）+ i18n `agents.engine.pi.title`。`DevicePicker` 已按 `agentKind` 过滤设备，自动生效。
- ✅ `npm run build`；后台能选 pi、要求选模型、提交后 `config.agent_kind="pi"`

### Phase 6 — 验收 & PR
- `make check`（AGENTS.md 必跑）
- 飞书 @ pi agent：流式正文 / 工具步骤 / 思考折叠 / 完成卡（成本可能空）/ 同话题续聊（第二句带 `--session`）
- 托管 skill 真加载；mcp 提示 unsupported
- worktree → `origin/main` 提 PR
