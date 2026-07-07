# Spec & Memory module design

## Context

Parsar currently lacks a centralized mechanism to inject "engineering
conventions / user preferences / project background" into every agent
conversation. Today the user has to tell the agent things like "our project
uses Go + gin" or "stop using Promise.all" in every chat, which causes:

1. **Repeated effort** — no shared context across sandboxes / sessions / agents.
2. **Rule drift** — verbal conventions get lost by the next turn.
3. **Expensive cold start** — the first few turns on a new project_agent are always spent feeding background.

Claude Code's auto-memory and external products like Trellis have proven
that "persist spec/memory + auto-inject" is a working product shape. This
proposal builds an equivalent capability in-house on parsar, **does not
import external code** (avoids AGPL + copying concerns), **the MVP focuses
on spec + memory**, and **covers claude code / opencode / codex**.

Expected outcome: users maintain a workspace-level spec plus user/project
memory in the UI; every sandbox injects them automatically; the agent
writes back newly-discovered rules on its own; sandboxes naturally share
context.

---

## Summary of design decisions (22 aligned with the user)

| # | Decision | Choice |
|---|------|------|
| 1 | Source of truth | DB-first (server is the sole authority; sandboxes are consumers + write-back clients) |
| 2 | MVP scope | spec + memory only (no tasks / workflow yet) |
| 3 | MVP platforms | claude code / opencode / codex |
| 4 | spec granularity | Multiple fragments (flat title + body + tags, not a file tree) |
| 5 | spec scope | workspace level |
| 6 | memory scope | user + project levels, two independent kinds |
| 7 | 4 memory types | user / feedback / project / reference |
| 8 | Injection strategy | Full injection (MVP); can evolve into tag-based smart injection later |
| 9 | Injection timing | SessionStart full + per-turn incremental |
| 10 | Meaning of per-turn | Deltas written by others after session start (incremental, not another full injection) |
| 11 | Codex | Degraded to SessionStart only (no per-turn hook) |
| 12 | Write-back path | The `parsar` CLI inside the sandbox |
| 13 | Trigger for memory writes | Agent-initiated (system-prompt meta-instructions + agent calls `parsar memory add`) |
| 14 | Approval flow | Direct write + audit (no approval flow) |
| 15 | CLI auth | Reuse runtime pairing → runner_credential (extend the existing mechanism) |
| 16 | Preloaded content | Only preload the hook scripts + the `parsar` CLI; no preloaded CLAUDE.md / AGENTS.md |
| 17 | spec cold start | Handwritten + agent write-back + import — three paths |
| 18 | Import source (MVP) | Only text paste; the backend splits into fragments |
| 19 | CLI name | `parsar` (matches `parsar-daemon-*` convention) |
| 20 | Table design | New `spec_fragments` + `memories`; audit reuses `audit_records` |
| 21 | Platform coverage | Every hook / plugin adapts per platform |
| 22 | No Trellis code | Build in-house; avoid AGPL + copying concerns |

---

## 1. Data model

### 1.1 Design principle: enum values are managed on the code side

All "string fields with a bounded set of values" (`source` / `scope` /
`memory_type`, etc.) use `TEXT NOT NULL` at the DB layer and **no
`CHECK (col IN (...))` constraint**. Reasons:

- Adding a value does not need a migration (schema evolution is not blocked on enums).
- Constants centralize on the code side; validation lives at handler / CLI entry points.
- If we later want to split `source='agent'` into `source='agent:auto'` / `source='agent:proposed'`, the DB stays out of the way — we change the code enum + handler validation.

**Go-side enum constants (`server/internal/specmemory/types.go`):**

```go
type Source string
const (
    SourceManual     Source = "manual"
    SourceAgent      Source = "agent"
    SourceImport     Source = "import"
    SourceUser       Source = "user"
    SourceAutoReview Source = "auto-review"   // reserved for phase-2 review fallback; MVP does not write but the constant is reserved
)
func (s Source) Valid() bool { ... }

type Scope string
const (
    ScopeUser    Scope = "user"
    ScopeProject Scope = "project"
)

type MemoryType string
const (
    MemoryTypeUser      MemoryType = "user"
    MemoryTypeFeedback  MemoryType = "feedback"
    MemoryTypeProject   MemoryType = "project"
    MemoryTypeReference MemoryType = "reference"
)
```

Handlers / CLI call `.Valid()` on incoming values; sqlc-generated model
fields are typed `string`, and the service layer converts between
Source/Scope/MemoryType.

**Structural constraints between fields** (scope='project' requires
project_id) are kept in the DB, because they are not enum constraints
but relational-integrity constraints and have nothing to do with
extensibility.

### 1.2 New table `spec_fragments`

Workspace-level, flat multi-fragment (each fragment is independently
editable and injectable).

```sql
CREATE TABLE spec_fragments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  body            TEXT NOT NULL,
  tags            TEXT[] NOT NULL DEFAULT '{}',
  source          TEXT NOT NULL,              -- managed via specmemory.Source on the code side
  created_by      UUID REFERENCES users(id),  -- NULL when written by an agent
  agent_actor     TEXT,                       -- when written by an agent, record connector name + project_agent_id
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at      TIMESTAMPTZ                 -- soft delete
);
CREATE INDEX idx_spec_fragments_workspace ON spec_fragments(workspace_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_spec_fragments_tags ON spec_fragments USING GIN(tags) WHERE deleted_at IS NULL;
```

### 1.3 New table `memories`

User-level and project-level share one table, distinguished by `scope`.

```sql
CREATE TABLE memories (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope           TEXT NOT NULL,              -- managed via specmemory.Scope on the code side
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id      UUID REFERENCES projects(id) ON DELETE CASCADE, -- non-null when scope='project'
  memory_type     TEXT NOT NULL,              -- managed via specmemory.MemoryType on the code side
  title           TEXT,                       -- optional short title
  body            TEXT NOT NULL,              -- body
  why             TEXT,                       -- recommended for feedback/project types
  tags            TEXT[] NOT NULL DEFAULT '{}',
  source          TEXT NOT NULL,              -- managed via specmemory.Source on the code side
  agent_actor     TEXT,                       -- when written by an agent, record connector name + project_agent_id
  conversation_id UUID REFERENCES conversations(id), -- link to the conversation when written by an agent
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at      TIMESTAMPTZ,
  -- Only keep the structural constraints between fields (guards relational integrity; unrelated to extensibility)
  CONSTRAINT project_scope_requires_project_id
    CHECK ((scope = 'user' AND project_id IS NULL) OR (scope = 'project' AND project_id IS NOT NULL))
);
CREATE INDEX idx_memories_user ON memories(user_id, scope) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_project ON memories(project_id) WHERE deleted_at IS NULL AND scope = 'project';
```

### 1.4 Audit (reuses `audit_records`)

Use the existing table from `server/migrations/000001_init.sql:1055-1102`;
new event_types:
- `spec_fragment.created` / `.updated` / `.deleted`
- `memory.created` / `.updated` / `.deleted`
- `spec.injected` / `memory.injected` (optional, for debug)

`actor_type` is `user` / `agent`; `actor_id` corresponds to
`user_id` / `project_agent_id`. Write via the existing
`server/internal/audit/postgres.go` `PostgresSink.Write()` interface.

### 1.5 Migration file location

Create `server/migrations/000005_spec_memory.sql`; follow the goose
format (reference the existing `000003_capability_canonical.sql`).

---

## 2. Server module

### 2.1 New package `server/internal/specmemory/`

Responsibilities:
- DB operations (CRUD on spec_fragments / memories).
- Injection-data assembly (render DB content into prompt sections).
- Import service (paste text → split into fragments).
- Audit logging.

Suggested file layout:
```
server/internal/specmemory/
  types.go           # enum constants (Source / Scope / MemoryType) + business-level structs
  store.go           # sqlc wrapping + business-level CRUD (string ↔ enum conversion)
  injector.go        # injection-data assembly (SnapshotSpec / SnapshotMemory / IncrementalMemory)
  importer.go        # split text into fragments
  prompts.go         # prompt-side templates (section titles, format, meta-instructions)
  service.go         # public interface, called by the connector and handlers
```

sqlc queries live at `server/internal/db/queries/spec_fragments.sql` and
`memories.sql` (following the current sqlc convention, cf.
`audit_records.sql`).

### 2.2 Injection hookup

**OpenCode connector** — `server/internal/connector/opencode/connector.go:Prompt()` L444:
after `stringFrom(mergedConfig, "system_prompt")` on line 529 has the raw
system_prompt, append what
`specmemory.Service.RenderInjection(ctx, workspaceID, userID, projectID, ...)`
returns, either at the end of system_prompt or inside a fixed marker block.

**AgentDaemon connector** — `server/internal/connector/agentdaemon/model_injection.go:renderStaticAgentOptions()` L163-183:
after `system_prompt` and `override_system_prompt` are extracted, add a
third source `spec_memory_injection` at the lowest precedence (overridden
by override). Pass through the `agent_options` map into
`PromptRequestPayload.AgentOptions` and on into the sandbox.

**Injection shape:**

```go
type Injection struct {
    SpecBlock          string  // <spec> ... </spec>
    MemoryBlock        string  // <memory> ... </memory>
    MemoryWriteGuide   string  // <memory-write-guide> ... </memory-write-guide>
    IncrementalMemory  string  // per-turn only; contains only what was added after session start
}
```

`SessionStart` injects SpecBlock + MemoryBlock + MemoryWriteGuide (full).
`per-turn` injects IncrementalMemory only (delta).

### 2.3 Injection section format (important)

See templates in `prompts.go`. Fixed structure:

```
<spec workspace="{workspace_name}">
### {fragment.title}
{fragment.body}
{if tags}[tags: {tags}]{/if}

### {fragment.title}
...
</spec>

<memory>
## user
- {body}
## feedback
- {body} (Why: {why})
## project
- {body} (Why: {why})
## reference
- {body}
</memory>

<memory-write-guide>
You have a persistent memory via `parsar memory add --type X --body "..." [--why "..."]`.

Types:
- user: user role, preferences, long-term goals
- feedback: explicit user corrections / confirmed non-obvious decisions (must include --why)
- project: current project background, milestones, decision rationale (must include --why)
- reference: external pointers such as dashboards, docs, Slack channels

When to save:
Whenever the user reveals stable information that will help future conversations. Save quietly; do not announce it in the chat.

When NOT to save:
- Anything inferable from the code / git history
- Bug-fix recipes (the code is the answer)
- Ephemeral task context
</memory-write-guide>
```

### 2.4 HTTP / gRPC API

New handlers (following the current `server/internal/handler/` pattern):

```
GET    /api/v1/workspaces/:wid/spec/fragments
POST   /api/v1/workspaces/:wid/spec/fragments
PATCH  /api/v1/workspaces/:wid/spec/fragments/:id
DELETE /api/v1/workspaces/:wid/spec/fragments/:id
POST   /api/v1/workspaces/:wid/spec/import      # body: { text: "..." }

GET    /api/v1/memories?scope=user
GET    /api/v1/memories?scope=project&project_id=...
POST   /api/v1/memories
PATCH  /api/v1/memories/:id
DELETE /api/v1/memories/:id

# Called by the sandbox CLI / hook
GET    /api/v1/agent-runtime/injection/snapshot?workspace_id=&user_id=&project_id=
GET    /api/v1/agent-runtime/injection/incremental?session_id=&since=
POST   /api/v1/agent-runtime/spec/fragments     # agent write; no user_id
POST   /api/v1/agent-runtime/memories            # agent write; no user_id
```

`/api/v1/agent-runtime/*` uses runner_credential auth (see 3.3); the rest
use the existing user-session auth.

---

## 3. Sandbox integration

### 3.1 `parsar` CLI design

Go binary at `/usr/local/bin/parsar` (co-located with `parsar-daemon`).
Source under a new `cmd/parsar/` (independent binary; shares models with
the server).

Subcommands:
```
parsar spec list [--tag X]
parsar spec add --title "..." --body "..." [--tag a,b]
parsar spec edit <id> [--title ...] [--body ...] [--tag ...]
parsar spec rm <id>

parsar memory list [--scope user|project] [--type user|feedback|project|reference]
parsar memory add --type <type> --body "..." [--title ...] [--why ...] [--tag a,b]
parsar memory edit <id> ...
parsar memory rm <id>

parsar sync                        # re-pull the injection snapshot (debug)
parsar --version
```

Environment variables (injected by the sandbox provider):
```
PARSAR_SERVER_URL=https://api.parsar.internal
PARSAR_RUNNER_TOKEN=<pairing-derived token>
PARSAR_RUNTIME_ID=<uuid>
PARSAR_WORKSPACE_ID=<uuid>
PARSAR_USER_ID=<uuid>
PARSAR_PROJECT_ID=<uuid>            # may be empty
PARSAR_CONNECTOR=claude|opencode|codex
PARSAR_PROJECT_AGENT_ID=<uuid>
PARSAR_CONVERSATION_ID=<uuid>
```

The CLI hits `/api/v1/agent-runtime/...` endpoints with header
`Authorization: Bearer $PARSAR_RUNNER_TOKEN`.

### 3.2 Hook scripts (three platforms, one set each)

Preinstalled in the image at `/opt/parsar/hooks/`; on startup for each
platform the server generates `.claude/settings.json` or an equivalent
config pointing at these scripts.

**Claude Code (Python):**
```
/opt/parsar/hooks/claude/session-start.py        # calls /injection/snapshot; returns hookSpecificOutput
/opt/parsar/hooks/claude/user-prompt-submit.py   # calls /injection/incremental; emits additionalContext
```

Configured via `~/.claude/settings.json` or
`/workspace/.claude/settings.json` (generated by the server, see 3.4):
```json
{
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "/opt/parsar/hooks/claude/session-start.py", "timeout": 10}]}],
    "UserPromptSubmit": [{"matcher": "*", "hooks": [{"type": "command", "command": "/opt/parsar/hooks/claude/user-prompt-submit.py", "timeout": 5}]}]
  }
}
```

**OpenCode (JS plugin):**
```
/opt/parsar/hooks/opencode/session-injection.js   # registers the first chat.message hook to attach the snapshot
/opt/parsar/hooks/opencode/per-turn-injection.js  # registers a tool or message hook to attach the delta
```

Configured via `~/.config/opencode/config.toml` or `.opencode/config.toml`
pointing to the plugin path (generated by the server). The plugin calls
the server endpoint via `fetch()`.

**Codex (degraded path):**
- No per-turn; SessionStart only.
- When the sandbox starts, the server generates `~/.codex/AGENTS.md`; its content = the snapshot injection (spec + memory + write-guide).
- No hook scripts.
- The agent can still call `parsar memory add` to write back (CLI is uniform).

**All hook scripts depend on the `parsar` CLI or `curl` directly** to
call the endpoint; server communication goes through
`PARSAR_RUNNER_TOKEN`. Prefer letting the hook call `parsar inject
snapshot` / `parsar inject incremental` to get data, and have the hook
only pipe the CLI stdout into the format the platform expects (JSON on
stdout / additionalContext / etc.). That way future changes to the
injection logic touch only one Go CLI, not three hook scripts.

### 3.3 Auth (reuse runtime pairing)

Refer to the previously aligned design — add
`runtime_type = 'agent_runtime'` (matching the aligned user feedback:
*"use the runtimes pairing_token → runner_credential flow; add a
runtime_type"*).

**Flow:**
1. `SandboxProvider.Acquire()` calls `CreateRuntimePairing()` when creating a sandbox and issues a token for `parsar-daemon` **plus another one for the `parsar` CLI** (two derived tokens on the same runtime, or two independent runtimes).
   Recommended: **one runtime issuing a single token used by both the daemon and the CLI**, with a scope field indicating the permission range.
2. The token is passed into the sandbox via `PARSAR_RUNNER_TOKEN` (the server provider exports it in `RunCommand()`).
3. Server-side, add middleware `auth.RunnerCredentialMiddleware` on
   `/api/v1/agent-runtime/*` paths that checks the token against
   `runtimes.pairing_token_hash` and injects the associated workspace / project / user into the request context.

**New store method** (next to the existing
`CreateRuntimePairing()` in `server/internal/store/store.go`):
```go
ValidateRunnerToken(ctx, token) (RuntimeIdentity, error)
// RuntimeIdentity: { RuntimeID, WorkspaceID, UserID, ProjectID, ProjectAgentID, Scope }
```

### 3.4 Dockerfile changes

File: `infra/e2b-templates/parsar-daemon-claudecode/e2b.Dockerfile`

New content (after the existing parsar-daemon install section):
```dockerfile
# parsar CLI
ARG PARSAR_VERSION=...
RUN curl -fSL "$GITLAB_URL/parsar-$PARSAR_VERSION-linux-amd64" -o /usr/local/bin/parsar \
    && chmod +x /usr/local/bin/parsar

# Hook scripts (COPYed from the build context; source at infra/e2b-templates/parsar-daemon-claudecode/hooks/)
COPY hooks/claude /opt/parsar/hooks/claude
COPY hooks/opencode /opt/parsar/hooks/opencode
RUN chmod +x /opt/parsar/hooks/claude/*.py
```

Hook script sources go under
`infra/e2b-templates/parsar-daemon-claudecode/hooks/{claude,opencode}/`.

**`.claude/settings.json` / opencode config are not seeded into the
image**; instead, after the sandbox starts, the server calls e2b Exec to
write them (they contain runtime-specific values). Extend the
`RunCommand()` chain after `SandboxProvider.Acquire()` to write these
config files before starting `parsar-daemon connect`.

### 3.5 Sandbox lifecycle changes

File: `server/internal/connector/agentdaemon/sandbox_provider.go`

Insert one step into the current `Acquire()` chain:
1. `CreateRuntimePairing()` ← existing
2. `e2b.Create()` ← existing
3. **New**: `seedPlatformConfig(ctx, sandbox, connector, runtimeContext)` — calls e2b Exec to write `.claude/settings.json` or the equivalent config
4. `RunCommand(parsar-daemon connect ...)` ← existing; add `PARSAR_*` env vars
5. `WaitForDevice()` / `Binder.Bind()` ← existing

Put the helper `seedPlatformConfig` in `sandbox_provider.go` or a new
`sandbox_seed.go`.

---

## 4. Write-back flow

### 4.1 Agent proactively writes memory

1. The `<memory-write-guide>` block injected on SessionStart teaches the agent when to write.
2. During a tool call, the agent runs `parsar memory add --type feedback --body "..." --why "..."`.
3. The `parsar` CLI hits `POST /api/v1/agent-runtime/memories` with the token header.
4. Server validates the token → writes to `memories` → writes to `audit_records` (`actor_type=agent`, `actor_id=project_agent_id`).
5. On the next user prompt, the per-turn hook pulls the delta and the new memory reaches the prompt automatically.

### 4.2 User edits spec / memory in the UI

1. The UI calls `/api/v1/workspaces/:wid/spec/fragments` etc. (via user-session auth).
2. Server writes to the tables + audit.
3. On the next SessionStart, the new content is injected automatically.
4. In-progress sessions pick it up via the per-turn incremental (injected in the incremental section).

### 4.3 Conflict handling (MVP simplification)

- spec_fragments / memories are fine-grained records; single-row updates use optimistic locking (`updated_at` as version).
- Concurrent edits on the same row: last writer wins; the UI shows "content was updated by someone else".
- No git-style merge, because the granularity is already fine.

---

## 5. Cross-platform adaptation cheatsheet

| Dimension | Claude Code | OpenCode | Codex |
|------|-------------|----------|-------|
| SessionStart injection | Hook script `additionalContext` | Plugin `chat.message` hook | `AGENTS.md` startup file |
| Per-turn incremental injection | Hook script `UserPromptSubmit` | Plugin `chat.message` hook | **Not supported (degraded)** |
| Injection location | Before system prompt / `<context>` block | Message parts | AGENTS.md |
| Config file location | `.claude/settings.json` | `~/.config/opencode/config.toml` | `~/.codex/AGENTS.md` |
| Config generation timing | Written by the server after sandbox start | Written by the server after sandbox start | Written by the server after sandbox start (whole AGENTS.md) |
| Hook script language | Python (JSON on stdin/stdout) | JS (plugin factory) | None |
| Hook calls the server via the parsar CLI | ✅ | ✅ | N/A |
| Write-back path for memory | `parsar memory add` | `parsar memory add` | `parsar memory add` |

---

## 6. MVP scope & non-goals

**Do:**
- Two tables (spec_fragments / memories) + audit reuse.
- Workspace-level spec UI (list + detail editing + tags).
- User / project memory UI (categorized list + editing + audit entry).
- Text-paste import (backend splits fragments simply by H2/H3).
- Full `parsar` CLI subcommands.
- Claude / OpenCode SessionStart + per-turn hooks.
- Codex SessionStart injection (AGENTS.md generation).
- Runner-credential auth extension.
- Meta-instruction for agents to write memory proactively.

**Do not (deferred):**
- Tag-based smart injection (only full for now).
- Post-hoc LLM review fallback (agent-initiated only).
- Import sources: file upload / repo scan (text-paste only).
- Trellis-style directory-structured spec (flat fragments only).
- Memory version history / rollback.
- Team-level memory sharing (only user / project).
- Approval flow (direct write + audit).

---

## 7. Landing checklist (independently assignable)

### Phase 1: data layer (must go first)
1. Write migration `server/migrations/000005_spec_memory.sql` (spec_fragments + memories + constraints + indexes).
2. Write sqlc queries `server/internal/db/queries/spec_fragments.sql` + `memories.sql`.
3. Run `make sqlc` to generate Go code.

### Phase 2: server business layer
4. Create package `server/internal/specmemory/` (types / store / injector / importer / service / prompts). **types.go first: define the Source / Scope / MemoryType enum constants + `Valid()` — every downstream module depends on them.**
5. Implement the importer: split simply by markdown H2/H3 (for import).
6. Implement the injector: render SpecBlock / MemoryBlock / MemoryWriteGuide / IncrementalMemory templates.
7. Hook up writes to `audit_records` (use the existing `audit.PostgresSink`).

### Phase 3: auth extension
8. Extend the value set of `runtimes.runtime_type` (add 'agent_runtime') — or confirm the current field can be reused.
9. Add `ValidateRunnerToken(ctx, token) (RuntimeIdentity, error)` to the store.
10. Add middleware `server/internal/middleware/runner_credential.go`; attach it to the `/api/v1/agent-runtime/*` route.

### Phase 4: HTTP handlers
11. UI-side spec / memory CRUD handlers (user-session auth).
12. Agent-runtime-side inject snapshot / incremental / write-back handlers (runner-credential auth).
13. Import handler.

### Phase 5: injection hookup (connector changes)
14. OpenCode connector `Prompt()`: call `specmemory.Service.RenderInjection()` at the system_prompt concat site.
15. AgentDaemon connector `renderStaticAgentOptions()`: add `spec_memory_injection` as the third source.
16. Unit tests covering injection-string concatenation.

### Phase 6: `parsar` CLI
17. Create `cmd/parsar/main.go`; implement subcommands (existing cobra or similar).
18. CLI reuses server model structs; HTTP calls carry the token.
19. Cross-compile (linux/amd64 + linux/arm64), publish to GitLab artifact.

### Phase 7: sandbox image & hooks
20. Modify `infra/e2b-templates/parsar-daemon-claudecode/e2b.Dockerfile` to install the `parsar` binary + COPY the hook script directory.
21. Create `infra/e2b-templates/parsar-daemon-claudecode/hooks/claude/{session-start.py, user-prompt-submit.py}`.
22. Create `infra/e2b-templates/parsar-daemon-claudecode/hooks/opencode/{session-injection.js, per-turn-injection.js}`.
23. Rewrite the three-platform hooks uniformly to "call `parsar inject ...` for data" (injection logic centralizes in Go).

### Phase 8: sandbox lifecycle changes
24. Add `seedPlatformConfig()` to the `Acquire` chain in `sandbox_provider.go`; write platform-specific configs.
25. Update `RunCommand(parsar-daemon ...)` to inject the full `PARSAR_*` env set.

### Phase 9: UI
26. Spec fragment list + detail editing (add a tab to the workspace settings page).
27. Memory list (split user / project) + detail + audit entry.
28. Import textarea + split preview + save.

### Phase 10: validation & rollout
29. Integration test: for each platform, run one SessionStart injection + agent-invoked `parsar memory add` write-back.
30. Docs: README / user guide / internal developer docs.

Dependencies: Phase 1 → 2 → (3 || 4 || 5) → 6 → (7 || 8) → 9 → 10.
Phases 3, 4, 5 can run in parallel; 6 depends on 3; 7, 8 depend on 6; 9
depends on 4.

---

## 8. Key file locations

| Purpose | File |
|------|------|
| Migration | `server/migrations/000005_spec_memory.sql` (new) |
| sqlc queries | `server/internal/db/queries/spec_fragments.sql`, `memories.sql` (new) |
| Business package | `server/internal/specmemory/` (new) |
| Auth middleware | `server/internal/middleware/runner_credential.go` (new) |
| Injection hookup (OpenCode) | `server/internal/connector/opencode/connector.go:529` (change) |
| Injection hookup (AgentDaemon) | `server/internal/connector/agentdaemon/model_injection.go:163-183` (change) |
| Sandbox lifecycle | `server/internal/connector/agentdaemon/sandbox_provider.go` (change; add seedPlatformConfig) |
| Proto (no change) | `internal/agentdaemon/proto/outbound.go` (already supports the AgentOptions map) |
| Audit hookup (no schema change) | `server/internal/audit/postgres.go:24-50` current `PostgresSink.Write()` |
| Dockerfile | `infra/e2b-templates/parsar-daemon-claudecode/e2b.Dockerfile` (change) |
| Hook scripts | `infra/e2b-templates/parsar-daemon-claudecode/hooks/{claude,opencode}/` (new) |
| CLI source | `cmd/parsar/` (new) |
| UI | Depends on the frontend layout; typically `web/src/pages/workspace/spec/` etc. (mirror the existing workspace settings page) |

---

## 9. Risks and open questions

### Risks
1. **Hook script failure fallback** — if `/injection/snapshot` times out or fails, does the hook return empty (degrade to no injection) or abort the session?
   Recommendation: return empty + server writes an audit + sandbox logs a warning; the session continues.
2. **Token leakage** — `PARSAR_RUNNER_TOKEN` is readable by every process in the sandbox.
   Mitigation: short TTL (1h) + revoke on sandbox close + restrict to `/api/v1/agent-runtime/*` paths only.
3. **Injection size bloat** — with hundreds of user memories the system prompt gets big.
   MVP does no truncation; phase 2 filters by tag / time window.
4. **Concurrent same-project connectors** — multiple sandboxes running on the same project; memory-delta sync frequency needs testing.
   Approach: the per-turn hook takes a `since` parameter (last pull time) as an incremental cursor.
5. **Codex degraded UX** — no per-turn; memories the agent writes back mid-session are not visible in the same session.
   Approach: append "New memory will take effect in the next session" to the AGENTS.md injected on Codex; acknowledge this as a known limitation.

### Open questions (defer to implementation; do not block the plan)
1. **Source of session_id** — how is the Conversation ID passed into the sandbox as an env var? Does every per-turn hook also need a `since` cursor?
2. **Import splitting strategy** — H2 vs H3 vs LLM? MVP does H2 first + user-preview edit.
3. **Audit retention / who can query** — reuse the existing audit_records policy; out of scope for this proposal.
4. **Is the memory `why` field mandatory** — recommended for feedback / project types, but the schema does not enforce (not NOT NULL); the CLI / UI guides the user.
5. **Whether user/project memory needs an importance / sort field** — decide in phase 2; MVP sorts by `updated_at desc`.

---

## 10. Verification (how to prove it works)

### 10.1 Unit tests
- `specmemory/injector_test.go`: given a set of fragments + memories, the rendered string matches the template.
- `specmemory/importer_test.go`: given a markdown blob, the fragment count / title / body split correctly.
- `middleware/runner_credential_test.go`: valid token / expired token / no token — three cases.

### 10.2 Integration test (three platforms)
Run an e2e script:
1. Create workspace + project + project_agent.
2. Add 2 fragments in the spec UI.
3. Add 1 user memory in the memory UI.
4. Start a sandbox (once each with claude / opencode / codex connectors).
5. First-turn prompt: have the agent restate the spec — expect it to reference fragment content.
6. User reply: "we use cursor pagination, not offset" — expect the agent to call `parsar memory add --type feedback`.
7. Check the `memories` table: the new row exists, `actor_type=agent`.
8. Second-turn prompt: have the agent restate memory — expect it to reference what it just learned.

### 10.3 Manual verification (Claude Code path first)
- `e2b spawn` a sandbox; exec `cat /workspace/.claude/settings.json` and confirm the generated config looks right.
- Exec `/opt/parsar/hooks/claude/session-start.py < empty.json`; confirm stdout is valid JSON.
- Exec `parsar memory list --scope user`; confirm the CLI works.
- Inside the sandbox, exec `parsar memory add --type user --body "test"`; verify the table row + audit.

### 10.4 Cross-platform sanity check
- With the same spec / memory across three platforms, diff the content actually landed in the model after prompt injection; confirm the core content matches (ignore platform-inherent differences).
- Token usage comparison: no injection vs with injection (MVP full); see the actual cost.
