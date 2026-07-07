# Integrating `pi` as a new agent_kind — integration guide

> Goal: bring [`@earendil-works/pi-coding-agent`](https://github.com/earendil-works/pi) (the interactive coding-agent CLI from Pi Agent Harness) into Parsar as the next `parsar-daemon` **`agent_kind = "pi"`**.
>
> Audience: engineers / agents about to implement the adapter. First read [`AGENTS.md`](../../AGENTS.md) and [`docs/architecture.md`](../architecture.md).

---

## 0. Bottom line first

| Question | Answer |
|---|---|
| What is pi | An **interactive coding-agent CLI**, in the same family as claude_code / opencode / codex |
| Where to integrate | **Scenario A: new `agent_kind`**, reusing the existing `agent_daemon` connector. **Not** a new `connector_type` |
| Can it be wrapped headless | **Yes**. pi has mature `--mode json` (single-shot NDJSON stream) and `--mode rpc` (long-running) TUI-less modes |
| MVP change scope | **Daemon-only**: new adapter package + 8 lines wired in `connect.go`. **Zero server-side changes on the dispatch path** (in BYO-key mode) |
| Server / web changes | **Optional**: only if you need "Parsar-managed model injection", "capability (skill/MCP) rendering", or "web-admin dropdown to create a pi agent" |
| DB migration | **Not required**. `agent_kind` lives in the `agents.config` JSONB with no CHECK constraint; the heartbeat's `supported_agent_kinds` is also JSONB |

One line: **pi integration = "clone the opencode adapter → change the CLI arg-building → change the stdout parsing"**, plus registration in `connect.go`.

> **Locked scope for this integration (agreed with the owner)**
>
> | Dimension | Decision |
> |---|---|
> | Daemon adapter (streaming body / thinking / tools / usage / resume) | Yes |
> | Model source | **Parsar-managed model injection** (not BYO env key) |
> | Managed skills | **Included** (pi's native Agent Skills, injected via `--skill`, same `SKILL.md` standard as claude_code) |
> | Managed mcp / plugin | No — pi has no MCP support → mark `ErrUnsupported` (like opencode / codex; users see "capability unsupported") |
> | AskUserQuestion human-in-the-loop | No (pi cannot; both Submits return the Unknown sentinel) |
> | Web-admin creation entry | **Included** (add the 4th engine card to the create wizard, matching codex / claude_code) |
> | DB migration | Not required |
>
> Consequently, the three "optional" items in §4.2 (managed model injection / managed skills / web admin) are **all required** this time. Detailed schedule at the bottom in **§9 Delivery plan**.

---

## 1. Abstraction recap (which layer to land in)

```
Server  ──(connector_type=agent_daemon)──> agent_daemon connector
                                              │  WebSocket(proto.Envelope)
                                              ▼
parsar-daemon: dispatch.Router ──(agent_kind)──> agent.Factory ──> agent.Session
                                                                     │
                                            claudecode / opencode / codex / 【new: pi】
```

You are implementing the lower `agent.Factory` + `agent.Session`
(`apps/parsar-daemon/internal/agent/registry.go:37` / `:41`). Contract:

- **`Factory(ctx, req proto.PromptRequestPayload, out chan<- proto.Envelope) (Session, error)`** — one prompt starts one session.
- **`Session` three methods:**
  - `Cancel(ctx) error` — idempotent, best-effort (SIGTERM → SIGKILL).
  - `SubmitPermission(ctx, permID, decision) error` — pi has no permission system → **return `agent.ErrUnknownPermission` directly**.
  - `SubmitPromptForUserChoice(ctx, askID, decision) error` — pi cannot → **return `agent.ErrUnknownAsk` directly**.
- **Channel ownership:** the adapter owns closing `out`. **You must** `close(out)` **exactly once** after sending the terminal `done` or `error` frame — the router uses this close as the "session ended" signal (`registry.go:5-15`).

---

## 2. pi-side facts you must know before implementing the adapter

> All from a local clone at `pi/` (gitignored — see repo-root `.gitignore` `/pi/`). Version `0.80.2`.

### 2.1 Binary and runtime prerequisites
- Command name **`pi`** (`pi/packages/coding-agent/package.json:9-11`, `bin.pi = dist/cli.js`).
- npm package `@earendil-works/pi-coding-agent`; **Node ≥ 22.19.0** (`package.json:97-99`).
- Install: `npm i -g @earendil-works/pi-coding-agent` (also supports pnpm/yarn/bun and a standalone binary via `bun build --compile`).

### 2.2 Headless single-shot mode (what the adapter uses)
```bash
pi --mode json -p "<prompt>"
```
- `--mode json` → stdout streams **NDJSON** (one JSON object per line), in real time (`pi/packages/coding-agent/src/modes/print-mode.ts:104-108`).
- When stdin is not a TTY, print mode is auto-entered (`main.ts:762-768`); the prompt can go through argv or a stdin pipe.
- **No `--cwd` flag**: working directory is `process.cwd()` (`main.ts:480`). Parsar sets it via `cmd.Dir = req.WorkDir` on the subprocess (opencode already does this).

### 2.3 NDJSON event stream (what you parse)
- The first line is typically the **session header**: `{"type":"session","version":3,"id":"<uuidv7>","cwd":"..."}` (`print-mode.ts:112-116`). → **Grab `id` for resume**.
- Then come `AgentEvent`s (`pi/packages/agent/src/types.ts:413-428`):

| Event `type` | Meaning | proto mapping |
|---|---|---|
| `agent_start` / `turn_start` | Lifecycle start | Ignore |
| `message_update` | Streaming delta, **with `assistantMessageEvent`** | See below |
| `message_end` | Message end; `message.usage` has tokens | `proto.TypeUsage` |
| `tool_execution_start` | Tool call start | `proto.TypeToolCall` (stage=`before`, optional) |
| `tool_execution_end` | Tool call end (`result`, `isError`) | `proto.TypeToolCall` (stage=`after`, optional) |
| `agent_end` | All done | Trigger `proto.TypeDone` |

- Shape of `message_update.assistantMessageEvent` (`pi/packages/ai/src/types.ts:453-457`):
  - `{type:"text_delta", delta:"...", partial:...}` → **body delta** → accumulate + emit `proto.TypeDelta`.
  - `text_start` / `text_end` → boundaries; skip for body (only use `delta`).
  - ⚠️ The union also contains reasoning/thinking and tool variants — **read the whole union starting at `types.ts:453`** before implementing, and map thinking variants to `proto.TypeThinking`.

### 2.4 Session resume
- pi persists sessions as JSONL: `~/.pi/agent/sessions/--<cwd>--/<ts>_<id>.jsonl` (`session-manager.ts:439-444`).
- Resume flags:
  - `--session <id|path>` — open by id/path (**recommended**).
  - `--session-id <id>` — use a specific id (creates if missing).
  - `--continue` / `-c` — resume the most recent session in the current cwd.
  - `--no-session` — pure in-memory, no persistence.
- **Parsar resume wiring** (matches claude_code): first turn without resume → grab `id` from the header → send it back in the `done` frame's `Metadata` (e.g. `{"pi_session_id": id}`) → server persists to `connector_session_bindings.metadata` → next turn goes down via `req.ResumeSessionID` → adapter appends `--session <id>`.
- Capability bit `Resume: true`.

### 2.5 Permissions / approvals
- **pi has no tool-level permission system**; no `--yolo` / `--auto-approve` (README "Permissions & Containerization", `project-trust.ts`).
- The only "approve" is **project trust** (whether to load a repo's `.pi/` config/extensions): `--approve` / `-a` trust, `--no-approve` / `-na` do not (`args.ts:180-182`).
- ✅ **Headless never hangs / prompts**: with `--mode json`, `hasUI=false`; a repo carrying `.pi/` is **silently treated as untrusted (false) → its `.pi/` is not loaded**, no interactive prompt appears (`project-trust.ts:86-88`; `package-manager-cli.ts:535` sets `hasUI: appMode==="interactive"`). pi never asks per-invocation approval for built-in `read/bash/edit/write` — it has no `--dangerously-skip-permissions` because there is no permission system to skip.
- **Safety default: still pass `--no-approve` explicitly** — not to avoid hangs (headless does not hang), but to pin the behavior: if a runtime's `~/.pi/agent/settings.json` had `defaultProjectTrust:"always"`, an arbitrary repo's `.pi/` extensions would silently load (= potential arbitrary code execution). Only enable `--approve` via config when you specifically want to load a project-level skill / extension.
- Capability bit `Permissions: false`. Parsar's permission_request / AskUserQuestion intercept paths **do not apply** to pi.

### 2.6 Auth / model selection
- Provider keys via env: `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GEMINI_API_KEY` / ... (`args.ts:335-377`); or `--api-key <key>` (must accompany `--model`, `main.ts:701-709`).
- Model selection: `--provider <name> --model <pattern>`, or the combined form `--model openai/gpt-4o`, `--model sonnet:high`; the default provider is `google` (`args.ts:238`).
- Config files: `~/.pi/agent/settings.json` (global), `<cwd>/.pi/settings.json` (project; deep-merged).
- **MVP (BYO key)**: set the `*_API_KEY` env var in the daemon host / sandbox; **do not touch server-side managed model injection**.

### 2.7 Other
- System prompt: `--system-prompt <text>` (full replacement) / `--append-system-prompt <text>` (append, repeatable) / `<cwd>/.pi/SYSTEM.md`.
- Tool switches: `--tools/-t`, `--exclude-tools/-xt`, `--no-tools`, `--no-builtin-tools`; built-ins are `read/bash/edit/write` (`grep/find/ls` are off by default).
- Thinking levels: `--thinking off|minimal|low|medium|high|xhigh`.
- **No MCP** (pi uses its own extension system). Parsar's MCP capability rendering is meaningless to pi.
- Version: `pi --version` prints the version and exits 0 (`main.ts:515-518`) — for the preflight.

---

## 3. Capability mapping (heartbeat descriptor to the server)

`proto.AgentKindCapabilities` (`internal/agentdaemon/proto/inbound.go:194`):

```go
proto.SupportedAgentKind{
    Kind: "pi",
    Capabilities: proto.AgentKindCapabilities{
        Streaming:   true,  // --mode json streams NDJSON
        Permissions: false, // no tool-level permission system
        Usage:       true,  // message_end carries usage (provider-dependent, may be incomplete)
        Resume:      true,  // --session / --continue
    },
}
```
`Available` is populated by the preflight from `pi --version`.

---

## 4. Change list (at a glance)

### 4.1 Must (daemon side, MVP-sufficient)

| # | File | Change |
|---|---|---|
| 1 | `apps/parsar-daemon/internal/agent/pi/` (new package) | `session.go` / `options.go` / `parser.go` / `version.go` + tests, **cloned from opencode** |
| 2 | `apps/parsar-daemon/internal/cli/connect.go` | 8 wiring points (see §6 Step 2) |

### 4.2 Optional (as needed)

| Trigger | File | Change |
|---|---|---|
| Need Parsar **managed model injection** (config has `model_id`) | `server/internal/connector/agentdaemon/model_injection.go:242` | `switch agentKind` add `case "pi":` + `injectPiManagedModel(...)`; default returns `ErrUnsupportedAgentKind` |
| Want to attach Parsar **managed skill/MCP/plugin/system_prompt** to a pi agent | `server/internal/connector/agentdaemon/capability_runtime.go:183` | `agentKindToRenderTarget` add `case "pi":` (+ `render.TargetPi`). **Skippable**: unknown kinds fall to `TargetClaudeCode` |
| Want a pi agent option in the **web admin** dropdown | `apps/web/src/pages/admin/CreateAgentDialog.tsx:42` etc. | Add `"pi"` to the `AgentEngine` union, add a ChoiceCard, keep `requiresModel` / `agentEngineFromAgent` in sync |

> Why BYO mode requires zero server changes: `injectManagedModel` returns nil early when no `model_id`/`default_model_id` is present (`model_injection.go:121-128`), never reaching the switch; on the dispatch path, `resolveAgentKind` only reads a string from config, and `validateAgentKindForSession` validates purely against the heartbeat broadcast — no hardcoded allowlist.

### 4.3 No change

- `registry.go` / `dispatch/router.go` — generic, driven by `RegisterKind` + `Resolve(agent_kind)`.
- `proto/outbound.go` / `proto/inbound.go` — wire schema is generic.
- DB / migrations — `agent_kind` lives in JSONB with no CHECK.

---

## 5. Data flow (one pi run)

1. Ops sets `agent_kind="pi"` in `agents.config` and binds a `runtime_id` (a device that has `parsar-daemon` running and `pi` available).
2. The server's `agent_daemon` connector's `resolveAgentKind` reads `"pi"` and checks against that device's heartbeat `supported_agent_kinds` to confirm availability.
3. The connector converts `PromptInput` into `proto.PromptRequestPayload` (`agent_kind="pi"`, `WorkDir`, `AgentOptions`, `ResumeSessionID`) and sends it over WS.
4. Daemon `dispatch.Router.handlePromptRequest` → `registry.Resolve("pi")` → `pi.Factory` creates a session and starts `pump`.
5. The session runs `pi --mode json -p ...`, parses NDJSON line by line, and writes `delta/thinking/tool_call/usage/done` uplink frames into `out`.
6. Terminal: emit `done` (`Content` = accumulated body, `Metadata` carries `pi_session_id`) then `close(out)` → the connector persists to DB and writes back to Feishu / Web.

---

## 6. Landing steps (step by step)

### Step 0: prerequisites
- ⚠️ **Per `AGENTS.md`: all code changes must land in a worktree cut from `origin/main`; direct commits on `main` are forbidden.**
  ```bash
  git fetch origin main
  git worktree add .worktrees/pi-agent-kind -b feature/pi-agent-kind origin/main
  ```
- Install pi on the target runtime: `npm i -g @earendil-works/pi-coding-agent`, self-check `pi --version`; configure provider key env vars.

### Step 1: new adapter package `apps/parsar-daemon/internal/agent/pi/`
`cp` the opencode package and edit file by file:

- **`version.go`** (simplest, do first) — mirror `opencode/version.go`:
  - `defaultBinary = "pi"`, `InstallURL = "https://pi.dev/docs/latest"`, `ErrCLINotFound`.
  - `CheckCLIAvailable(ctx, binary) (string, error)`: `LookPath` → run `pi --version` → return the trimmed version string.

- **`options.go`** — `BuildArgs(runID, prompt, workDir, opts map[string]any) (BuildResult, error)` builds the pi CLI:
  - Baseline `args := []string{"--mode", "json", "-p"}`, plus **`--no-approve`** (safety default; can be flipped to `--approve` via opts).
  - Resume: `req.ResumeSessionID != ""` → `--session <id>`.
  - Read opts keys (reuse Parsar's existing key names; unknown keys are ignored):

    | opts key | Type | → pi flag |
    |---|---|---|
    | `model_selector` / `model` | string | `--model <v>` (former wins; supports `provider/model`) |
    | `override_system_prompt` | string | `--system-prompt <v>` (full replacement) |
    | `system_prompt` | string | `--append-system-prompt <v>` (append; spec/memory injection lives here) |
    | `thinking` | string | `--thinking <v>` |
    | `allowed_tools` / `tools` | []string/csv | `--tools <csv>` |
    | `exclude_tools` | []string/csv | `--exclude-tools <csv>` |
    | `approve_project_trust` | bool | true → `--approve`, false/absent → `--no-approve` |
    | `env` | map[string]string | Inject into subprocess env (provider keys, etc.) |
    | `api_key` | string | `--api-key <v>` (only during managed model injection; requires `--model` as well) |
  - `WorkDir` resolution mirrors opencode: must be absolute or `~/`-prefixed, `MkdirAll` if missing; pass through `cmd.Dir` (pi has no `--cwd`).
  - Prompt is the `-p` arg (or via stdin).

- **`parser.go`** — the core. The translator `json.Unmarshal`s each line and dispatches by `type`:
  - `"session"` → record `header.id` (echoed back in terminal `done.Metadata["pi_session_id"]`).
  - `"message_update"` → read `assistantMessageEvent`: `text_delta` accumulates `delta` and emits `proto.TypeDelta`; reasoning/thinking variants emit `proto.TypeThinking`.
  - `"message_end"` → read `message.usage` and emit `proto.TypeUsage` (`Provider="pi"` or the actual provider).
  - `"tool_execution_start"`/`"_end"` → (optional) emit `proto.TypeToolCall` (stage before/after).
  - Non-JSON lines → plain buffer as a safety net (same as opencode: when no deltas ever arrive, emit the plain buffer as a single delta).
  - **Terminal**: after stdout EOF, `cmd.Wait()`; if `waitErr != nil` (and not a cancel) → emit `proto.TypeError` (with truncated stderr); **always** emit `proto.TypeDone` at the end (`Content` = accumulated body, `Metadata` = `pi_session_id` + `connector_path:"pi_run"`).

- **`session.go`** — mirror `opencode/session.go`:
  - `Factory` → `newSession`: `BuildArgs` → `exec.CommandContext(ctx, cfg.piBinary, args...)`, `cmd.Dir = workDir`, `cmd.Env = append(os.Environ(), buildRes.Env...)`, wire stdout/stderr, `Start`, then two goroutines `run(stdout)` + `pumpStderr(stderr)`.
  - `run()` defer chain guarantees `close(waitDone)` → `cleanup()` → `closeOut()`, **so `close(out)` happens exactly once**.
  - `Cancel()`: `SIGTERM`, then `killTimeout` (default 3s) → `SIGKILL`.
  - `SubmitPermission` returns `agent.ErrUnknownPermission`; `SubmitPromptForUserChoice` returns `agent.ErrUnknownAsk`.

- **`export_test.go` + `*_test.go`** — mirror opencode: use `TestMain` to make the test binary impersonate a fake `pi` (env var chooses the role: `json-success` / `plain` / `nonzero` / `hang`); assert delta+usage+done sequences, plain fallback, non-zero exit produces error+done, cancel closes out, empty prompt is rejected, nil out is rejected.

### Step 2: 8 wiring points in `connect.go`
Mirror opencode (`apps/parsar-daemon/internal/cli/connect.go`):

1. Import `piagent ".../internal/agent/pi"`.
2. Add `Pi proto.SupportedAgentKind` to `agentCLIDiscovery` (`:192`).
3. Add `Pi func(context.Context, string) (string, error)` to `agentCLIChecks` (`:198`).
4. Add `Pi: piagent.CheckCLIAvailable` to `defaultAgentCLIChecks` (`:204`).
5. In `discoverAgentCLIs`, add the initial descriptor `Pi: proto.SupportedAgentKind{Kind:"pi", Capabilities: {Streaming,Usage,Resume:true}}`.
6. In `discoverAgentCLIs`, add the pi version-detection block (mirroring opencode `:271-284`): set `Available/Version` on success; on `ErrCLINotFound`, print an install hint.
7. In the "no available CLI" fallback check (`:301`), add `&& !out.Pi.Available`.
8. In `registerAgentKinds` (`:307`), add `registry.RegisterKind(agentCLIs.Pi, piagent.Factory)`.

> The heartbeat's `supported_agent_kinds` is populated by `registry.SupportedAgentKinds()` automatically; no other changes needed.

### Step 3 (optional): managed model injection
When you want to configure `model_id` for pi in Parsar (rather than BYO env keys):
- Add `case "pi":` to the `switch` at `server/internal/connector/agentdaemon/model_injection.go:242`. New `injectPiManagedModel(opts, modelID, mr, apiKey)`: decode the resolved `apiKey` / `provider` / `model` into `opts` (the adapter then flips them into `--api-key` + `--model`).

### Step 4 (optional): web admin
`apps/web/src/pages/admin/CreateAgentDialog.tsx`: add `"pi"` to `AgentEngine`, add a ChoiceCard, keep `requiresModel` / `agentEngineFromAgent` in sync. **You can skip this** — you can create pi agents by writing `project_agents.config.agent_kind="pi"` directly via seed/SQL/API.

### Step 5: create a pi agent
- Fastest: on some `project_agents` row, set `config` JSONB to `{"agent_kind":"pi", ...}`; bind `runtime_id` to a device that has pi installed.
- On that device, `parsar-daemon connect`. Confirm the startup log shows pi `preflight ok (<version>)` and the heartbeat broadcast contains `pi`.

### Step 6: verify
```bash
make check                      # required by AGENTS.md
# Daemon package only:
go test ./apps/parsar-daemon/internal/agent/pi/...
go test ./apps/parsar-daemon/internal/cli/...
```
- Manual smoke test: on a pi-equipped machine run `pi --mode json -p "say hi"` and confirm the NDJSON matches what you parse.
- End-to-end: @-this pi agent in a Feishu group and confirm streaming reply + persistence + resume (the second message carries `--session`).

---

## 7. Common pitfalls (self-check before submission)

1. **`close(out)` exactly once**: every path (clean end / non-zero exit / cancel / subprocess-fail-in-factory) must emit the terminal frame and then close; otherwise the router's pump blocks forever.
2. **Headless does not hang on trust**: in headless (`hasUI=false`), a repo with `.pi/` is silently treated as untrusted and skipped, no prompt (`project-trust.ts:86-88`), no user approval needed. Still explicitly pass `--no-approve` to pin the behavior (defend against a global `defaultProjectTrust:"always"` silently loading repo extensions); only pass `--approve` when you specifically want to load a project skill / extension. **Do not** install `examples/extensions/permission-gate.ts` — that is the one thing that reintroduces approval prompts.
3. **No `--cwd`**: use `cmd.Dir`; if `WorkDir` is empty in sandbox mode, fall back to opencode's per-conversation scratch dir.
4. **Sessions archive by cwd**: pi's `--continue` relies on cwd stability; for Parsar resume, use **explicit `--session <id>`** (the id from the header), not `--continue`.
5. **Usage is provider-dependent**: `message_end.usage` may have partial or missing fields; parse defensively; `Usage` capability = true does not guarantee cost is present.
6. **Do not map capabilities that do not exist**: pi has no permissions / AskUserQuestion / MCP — `Permissions=false`, both Submits return the Unknown sentinel directly; do not synthesize non-existent flags.
7. **Read the full thinking union**: `assistantMessageEvent` is not just text; reasoning/thinking variants must map to `proto.TypeThinking`, otherwise thinking chains bleed into the body.
8. **Node version**: the runtime needs Node ≥ 22.19; when preflight `pi --version` fails, surface a clear install/upgrade hint (mirroring opencode's `InstallURL`).

---

## 8. Reference file index

**Parsar abstractions**
- `apps/parsar-daemon/internal/agent/registry.go:37/41/86/93/108/132` — Factory / Session / RegisterKind / Resolve
- `internal/agentdaemon/proto/inbound.go:194/204/215` — AgentKindCapabilities / SupportedAgentKind / Heartbeat
- `internal/agentdaemon/proto/outbound.go:37` — PromptRequestPayload

**Template (opencode adapter)**
- `apps/parsar-daemon/internal/agent/opencode/{session,options,parser,version}.go`
- `apps/parsar-daemon/internal/cli/connect.go:192/198/204/216/271-284/301/307`

**Optional server-side change points**
- `server/internal/connector/agentdaemon/model_injection.go:119/121-128/242`
- `server/internal/connector/agentdaemon/capability_runtime.go:183`
- `apps/web/src/pages/admin/CreateAgentDialog.tsx:42`

**pi upstream (local clone `pi/`, gitignored)**
- Entry / args: `packages/coding-agent/src/cli.ts:20`, `src/cli/args.ts:63`, `src/main.ts:100-111/480/515-518/762-768`
- Headless output: `packages/coding-agent/src/modes/print-mode.ts:104-116`
- Event types: `packages/agent/src/types.ts:413-428`, `packages/ai/src/types.ts:453-457`
- Sessions: `packages/coding-agent/src/core/session-manager.ts:439-444`
- Trust: `packages/coding-agent/src/core/project-trust.ts`
- Auth: `packages/coding-agent/src/cli/args.ts:335-377`

---

## 9. Delivery plan (locked scope this round)

> Scope is defined in the "Locked scope" box at the top. No DB migration. All changes happen in a worktree per `AGENTS.md`; run `make check` before submitting.
>
> **Dependency order**: Phases 1→2 are foundation (daemon runs, heartbeat broadcasts pi); Phases 3 / 4 / 5 are relatively independent and can run in parallel.

### Phase 0 — prerequisites
- `git fetch origin main && git worktree add .worktrees/pi-agent-kind -b feature/pi-agent-kind origin/main`
- Install pi on the target runtime: `npm i -g @earendil-works/pi-coding-agent` (Node ≥ 22.19), `pi --version` self-check.
- Smoke: `pi --mode json -p "say hi"`; confirm the NDJSON shape matches §2.3.

### Phase 1 — Daemon adapter (core, daemon-only, independently testable) ⟶ new `apps/parsar-daemon/internal/agent/pi/`
Clone the opencode package, edit file by file:
1. **`version.go`**: `defaultBinary="pi"`, `InstallURL`, `ErrCLINotFound`, `CheckCLIAvailable` (runs `pi --version`).
2. **`options.go`**: `BuildArgs` baseline `--mode json -p` + `--no-approve`; opts→flags mapping per the §6 Step 1 table, plus:
   - Managed-model output `model`→`--model <provider/model>`, `api_key`→`--api-key` (pi requires `--api-key` to accompany `--model`)
   - Managed-skill output `skills`→one `--skill <extracted dir>` each (extraction: #3)
   - `WorkDir`→`cmd.Dir` (pi has no `--cwd`); resume→`--session <id>`.
3. **`skills.go`** (new; reference the claude-side extraction logic): read `opts["skills"]` `{name,download_url,sha256}` → download the zip → verify sha256 → extract to scratch `<workDir>/.pi-skills/<name>/` → hand the directory path back to options for `--skill`.
4. **`parser.go`**: dispatch NDJSON line by line (§2.3): `session`→record id; `message_update.assistantMessageEvent`'s `text_delta`→`TypeDelta` (accumulate); reasoning/thinking variants→`TypeThinking`; `message_end.usage`→`TypeUsage` (cost defensively); `tool_execution_start/end`→`TypeToolCall`; EOF→`Wait()`, non-cancel err→`TypeError`, **always** `TypeDone` (Metadata carries `pi_session_id`); non-JSON lines fall through to plain.
5. **`session.go`**: Factory→newSession→`exec.CommandContext` + `cmd.Dir` + `cmd.Env`→run+pumpStderr; defer chain enforces `close(out)` exactly once; Cancel SIGTERM→3s→SIGKILL; `SubmitPermission`→`ErrUnknownPermission`, `SubmitPromptForUserChoice`→`ErrUnknownAsk`.
6. **`*_test.go` + `export_test.go`**: fake pi (json-success/plain/nonzero/hang), assert delta+usage+done, plain fallback, non-zero-exit error+done, cancel closes out, empty prompt rejected, nil out rejected.
- ✅ `go test ./apps/parsar-daemon/internal/agent/pi/...`

### Phase 2 — connect.go wiring ⟶ `apps/parsar-daemon/internal/cli/connect.go`
Follow opencode across 8 points (see §6 Step 2). Capabilities: `Streaming/Usage/Resume=true, Permissions=false`.
- ✅ `go test ./apps/parsar-daemon/internal/cli/...`; `parsar-daemon connect` logs show `pi preflight ok (<ver>)`, heartbeat contains pi.

### Phase 3 — managed model injection ⟶ `server/internal/connector/agentdaemon/model_injection.go:242`
Add `case "pi":` to `switch agentKind` → new `injectPiManagedModel(opts, modelID, mr, apiKey)`: decode model_id → (provider, model_key, api_key), set `opts["model"]="<provider>/<model_key>"` + `opts["api_key"]`; add a mapping from Parsar provider name → pi provider id (anthropic/openai/google/...).
- ⚠️ Verify: does the **backend API** for create-agent enforce an allowlist on `agent_kind`? (The dispatch path does not; the admin-create path needs confirmation. Add pi if so.)
- ✅ Server unit tests + end-to-end confirmation that the subprocess receives the right `--model` / `--api-key`.

### Phase 4 — managed skill render target ⟶ `server/internal/connector/agentdaemon/{render/*, capability_runtime.go}`
1. `render` package: add `TargetPi` constant + `piRenderer`: `KindSkill`→reuse claude's `{name,version,oss_key,sha256}` descriptor (same Agent Skills standard); `KindMCP` / `KindPlugin`→`ErrUnsupported`; `KindSystemPrompt`→share `renderSystemPrompt`; register in `render.For()`.
2. `capability_runtime.go:183` `agentKindToRenderTarget` add `case "pi": return TargetPi` (eliminate the unknown-kind fallthrough to `TargetClaudeCode`).
- ⚠️ For the first skill, verify the zip uses the standard `SKILL.md` layout (claude and pi share the standard, theoretically interoperable).
- ✅ Render unit tests; pi agent bound to a skill receives `--skill` correctly; binding an mcp shows the unsupported message.

### Phase 5 — web creation card ⟶ `apps/web/src/pages/admin/CreateAgentDialog.tsx`
Add `"pi"` to the `AgentEngine` union (`:42`); add pi to `agentEngineFromAgent` (`:60`); include pi in `requiresModel` (`:586`); add a 4th `<ChoiceCard>` to Step 2 (`:994`) + i18n `agents.engine.pi.title`. `DevicePicker` already filters devices by `agentKind`, so it works automatically.
- ✅ `npm run build`; the admin UI lets you pick pi, requires a model, and submits with `config.agent_kind="pi"`.

### Phase 6 — acceptance & PR
- `make check` (required by AGENTS.md).
- @-pi agent in Feishu: streaming body / tool steps / thinking fold / completion card (cost may be blank) / same-thread resume (second message carries `--session`).
- Managed skill actually loads; mcp shows the unsupported message.
- worktree → open a PR against `origin/main`.
