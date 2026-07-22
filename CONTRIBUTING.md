# Contributing to Parsar

Parsar is an open-source agent collaboration control plane for engineering
teams. This guide covers the rules that apply at every stage of development —
read it before opening a PR.

## Hard rules

- All runtime config, logs, state, and cache must be written under
  `~/.parsar/`.
- Never write runtime state to the repo root or the current working directory.
- A user-supplied working directory must be an absolute path or start with
  `~/`. Reject relative paths outright — do not resolve them against the CWD.
- Before any install / setup step, ask yourself: does this write to the
  user's current directory? If yes, fix it before shipping.
- Before any substantial implementation, refactor, runtime/process change,
  schema/API change, or cross-package behavior change, read this guide. If
  the change creates, removes, or clarifies an architecture rule, ownership
  boundary, workflow, required check, or generated artifact contract, update
  this guide in the same branch.
- Keep contributor docs concise and single-sourced. `AGENTS.md` is only the
  agent-facing shortcut; canonical rules live here. When two documents repeat
  the same long-form rule, delete the duplicate and link to the canonical
  section.

## Worktree workflow

All code changes — features, fixes, refactors, documentation that references
code paths — must happen in a git worktree branched from `main`. **Direct
commits to `main` are forbidden.**

`main` is the single integration baseline:

- A new worktree must branch from the latest `origin/main`. Run
  `git fetch origin main` first.
- `main` is the source of truth. Every worktree starts from it and lands
  back into it.
- After implementing and verifying, push the feature branch and open a PR
  against `main`. **Merging into `main` requires PR review** — local
  fast-forward or local merge to bypass review is not allowed.
- Run `make check` (and any relevant E2E target) before requesting review.
- Place worktrees under `.worktrees/<feature-name>/` so they don't litter
  the repo root.

```bash
git fetch origin main
git worktree add .worktrees/feature-name -b feature/name origin/main
```

Direct development on `main` is not allowed. Every session honours this rule.

## Architecture baseline

- **Server**: Go + Chi.
- **Database**: PostgreSQL only.
- **DB toolchain**: goose migrations + sqlc-generated queries + pgx/pgxpool
  at runtime.
- **Web**: Vite + React SPA, eventually served directly by the Go server.
- **API**: OpenAPI-first.
- **Connector MVP**: Agent Daemon Connector (`connector_type=agent_daemon`,
  adapter determined by `project_agents.config.agent_kind` — `opencode`,
  `claude_code`, …) plus the HTTP Agent Connector.
- Agent Daemon runs are dispatched to the `parsar-daemon` runtime bound to
  the agent (`project_agents.runtime_id`); the daemon's internal adapter
  picks which CLI actually executes.

## Architecture boundaries

The repo has several concepts that sound similar but must stay separate.
When adding or changing code, name the boundary explicitly in the PR
description and keep ownership on the side listed here.

### Install and image freshness

- The root `docker-compose.yml` must be directly runnable with
  `docker compose up -d`. Do not require `install.sh` to pre-generate `.env`
  values for mandatory services to boot. If a service needs a local-only
  shared secret, the compose file must provide a clearly documented dev-only
  default and allow production/Dokploy installs to override it with a stable
  random value.
- The one-command installer is both install and upgrade path. Default GHCR
  images must be pulled before `docker compose up` so `:latest` does not
  silently reuse a stale local image after `main` changes.
- `install.sh` may still write stable random overrides such as
  `PARSAR_MASTER_KEY` and `PARSAR_SHARED_RUNTIME_TOKEN` for safer local
  installs, but raw Compose/Dokploy deployments must not depend on those
  installer-only side effects.
- Keep `install.sh` a thin Compose wrapper. Its CLI is limited to installation
  location, web bind/port, image overrides, and validation. Uncommon deployment
  settings belong in the Compose environment rather than new installer flags.
- Services exposed through a deployment platform may gain an ingress network,
  but they must remain explicitly attached to the Compose `default` network
  when they depend on internal service DNS names such as `postgres`.
- DB-driven workspace connectors exposed in the admin UI must start and stop
  from their persisted `enabled` and event-mode settings. Do not add a second
  deployment environment flag after a connector is saved and marked enabled.
- The root Compose file contains deployment infrastructure only. Do not add
  Feishu, Slack, Discord, or other workspace connector credentials or enable
  flags there; those integrations are configured through the web UI and stored
  in the encrypted connector tables.
- Avoid spelling out image, application, or Docker defaults in Compose. Keep a
  field only when it changes behavior, connects services, persists state, or
  exposes an intentional operator override.
- The local compose file must express the same default with
  `PARSAR_IMAGE_PULL_POLICY=always` for Parsar-owned images. Local image
  testing must opt out explicitly with `PARSAR_IMAGE_PULL_POLICY=never`.
- Local development images stay opt-in through installer overrides such as
  `--image parsar:local` / `--sandbox-image parsar-sandbox:local`; do not make
  local tags the default path for end users.

### Runtime and execution concepts

- `connector_type` chooses the protocol Parsar uses to run an agent
  (`agent_daemon`, `http_agent`, ...). It does not say where the process
  runs.
- `runtime_id` chooses the concrete paired runtime/device/sandbox that will
  receive a run. It is a routing handle, not agent configuration.
- `agent_kind` chooses the daemon-side engine (`claude_code`, `codex`,
  `pi`, `opencode`). It is interpreted only by `parsar-daemon`.
- Placement labels such as local device, cloud sandbox, and external agent
  are UI/product concepts. Do not branch business logic on display copy.
  Derive placement from typed runtime/provider/config fields in one shared
  helper per layer.

### Server versus daemon ownership

- The server owns auth, workspaces, agent records, runtime bindings, run
  records, audit/usage persistence, and upstream engine session ids.
- `parsar-daemon` owns CLI discovery, process spawning, CLI-specific env,
  cwd selection inside its host/container, permission prompts, and translating
  CLI streams into Parsar daemon protocol frames.
- `internal/agentdaemon/proto` is the only shared wire contract between the
  server and daemon. The daemon must not import `server/internal/...`, and the
  server must not import daemon-internal adapter packages.
- Any state needed to recover a conversation after a server restart, daemon
  reconnect, or child-process exit must be stored durably by the server.
  In-memory maps may cache waiters or sockets only; they must not be the
  source of truth for conversation/session continuity.
- Work directory validation is a cross-boundary security rule: user input is
  accepted only as an absolute path or `~/...`; daemon-side fallbacks must stay
  under `~/.parsar/`.

### Agent CLI adapter contract

- Every daemon-side agent adapter must use a shared process runner for CLI
  subprocesses. New adapters must not hand-roll separate `Start`, stdin,
  cancellation, timeout, and `Wait` loops.
- Every subprocess must be waited/reaped. Cancellation must close stdin when
  appropriate, send a graceful signal first, and escalate to kill after a
  bounded timeout.
- A completed prompt closes its protocol stream immediately, but daemon-side
  CLI processes and their background children stay alive until the
  conversation has received no new prompt for one hour. A new prompt for the
  same `AgentStateKey` renews that idle window. Explicit cancellation, device
  shutdown, and daemon shutdown still terminate processes immediately.
- When an engine supports resume, persist the upstream session id through
  `agent_engine_sessions` and pass `AgentSessionID` plus `AgentStateKey` over
  the daemon protocol. Do not keep resume ids only in adapter memory, files
  without a server record, or frontend state.
- Adapter-specific state directories must be derived from `AgentStateKey`
  under `~/.parsar/`; never use the repo checkout, container image working
  directory, or the process CWD as hidden state.

### Human interaction lifecycle

- `agent_interactions` is the canonical durable record for permission prompts
  and `AskUserQuestion` / `requestUserInput` requests. The Web approval inbox,
  conversation SSE notices, and IM cards are presentation surfaces over that
  record.
- The active Web conversation renders its pending durable interactions as full
  decision cards. SSE request IDs may prioritize a newly emitted card, but the
  workspace interaction query must restore the card after refresh. The inbox
  remains the workspace-wide queue, and both surfaces reuse the same decision
  component and resolution API.
- `conversations.metadata.gateway_inflight.permission` and
  `prompt_for_user_choice` remain channel delivery slots only. Do not make a
  runtime response depend on an IM card having been rendered first.
- A daemon adapter that supports approval or user input must emit the shared
  protocol request and defer the engine response until
  `SubmitPermission` / `SubmitPromptForUserChoice` arrives. Adapters must not
  silently approve, deny, or synthesize empty answers as a fallback.
- Codex agents that may call `request_user_input` use `config.mode=plan`.
  Prompt wording cannot unlock the tool in default mode; the daemon must pass
  the configured mode through app-server `turn/start.collaborationMode`.
  Agent create/profile configuration owns this persisted value, accepts only
  `default` or `plan` for Codex, and clears it when switching engines.
- Daemon-originated approval and question envelopes keep `Envelope.ID` equal
  to the run ID so the server can deliver them to the active subscriber. Put
  the daemon-minted interaction handle in payload `request_id` / `ask_id`;
  legacy permission IDs in `Envelope.ID` are read only for decision-routing
  compatibility.
- Persist the run event and its derived `agent_interactions` row in one
  transaction before publishing an approval or question to SSE/IM surfaces.
  If that canonical write fails, abort the run instead of exposing an
  actionable card that cannot be claimed or recovered.
- Web and IM responders must call the same interaction resolution service.
  Routes and card callbacks must not implement their own status transition or
  deliver to the runtime before the canonical compare-and-swap claim succeeds.
- Question answers use the stable question ID from the adapter and preserve
  selected values as an array. Headers and positional answers are compatibility
  fields only; they are not durable identity.
- Preserve adapter question metadata end to end. `is_other=false` forbids a
  free-text answer, `is_secret=true` uses a masked input wherever that surface
  collects free text, and secret answer values may travel to the waiting
  runtime but must be redacted from
  `agent_interactions.response`, interaction-resolution run and audit events,
  logs, API reads, and rendered chat receipts or callback summaries.
- Every deferred request has a bounded lifetime. The server expiry worker is
  authoritative: it explicitly denies a permission or cancels user input,
  unblocks the runtime, and leaves an `expired` terminal record. Daemon timers
  are a safety net and must make the same deny/cancel choice. Neither path may
  silently continue the requested action.
- A server-to-daemon WebSocket write is not proof that the engine accepted a
  decision. The daemon must return an application-level decision ack after
  `SubmitPermission` / `SubmitPromptForUserChoice` succeeds; only then may the
  server persist the terminal interaction state. Missing or negative acks keep
  the canonical interaction retryable (except a definitive `not_pending` or
  replay `decision_conflict`, which closes it as runtime-gone).
- Every transport attempt uses a unique delivery ID so a late ack cannot
  satisfy a newer resolver. Daemon replay is keyed by runtime request plus the
  decision payload with that delivery ID excluded: identical retries are
  acknowledged without a second apply, while changed retries conflict.
- The decision-ack wire contract starts at agent-daemon protocol `0.2`.
  Server and daemon keep the existing strict major/minor handshake so a `0.1`
  peer fails closed and must be upgraded instead of applying an unacknowledged
  decision during a rolling-version mismatch.
- Human responses are workspace-scoped, reject viewer writes, claim a single
  winner before contacting the runtime, persist a terminal state, clear the
  matching inflight slot, and emit an approval audit event. A terminal run
  cancels any still-open interactions. Multi-pod daemon routing resolves
  `request_id` through the canonical interaction's `device_id`; IM slots are
  only a legacy fallback.

### Sandbox and local runtime lifecycle

- The default local install path provides one ready-to-use sandbox runtime.
  Do not require the Parsar server container to create sibling Docker
  containers through `/var/run/docker.sock` for normal first-run operation.
- Local Docker lifecycle, cloud sandbox lifecycle, and user-paired devices are
  different providers behind the same daemon protocol. Keep provider-specific
  create/renew/kill logic in `server/internal/sandbox/...` or a narrowly named
  runtime provider; do not spread Docker/E2B calls through handlers,
  connectors, or frontend components.
- Dynamic local sandbox scaling is not a product guarantee. If it is added,
  it must be owned by an explicit local supervisor/runtime provider with a
  reviewed Docker socket boundary, not by ad hoc server-side `docker run`
  calls.
- Eager acquisition must be best-effort. Failure to prewarm a sandbox should
  surface as runtime health/provisioning state, not crash unrelated startup
  paths.

### API, DB, and generated surfaces

- New persistent state starts with a migration and sqlc query. Avoid direct
  SQL embedded in route handlers or connector code unless the package already
  owns that persistence boundary and tests cover it.
- JSON config blobs are allowed only at integration boundaries where providers
  are genuinely schemaless. Once two call sites read the same key, introduce a
  typed parser/normalizer and make all callers use it.
- Frontend API shape mirrors must live in `apps/web/src/lib/` next to the API
  client/hook that owns them. Page components should receive typed values, not
  parse runtime/provider/config JSON themselves.
- Multi-endpoint model rows keep the default/legacy `models.base_url`, but
  protocol-specific runtime URLs belong in `models.config.endpoint_base_urls`
  keyed by `supported_endpoint_types` values such as `anthropic`, `openai`, and
  `openai-response`. Runtime injectors must consult the endpoint map before
  falling back to `models.base_url`.
- Generated files (`docs/openapi/openapi.yaml`, `server/internal/db/sqlc/*`)
  are committed artifacts, but never the source of truth. Change annotations
  or SQL first, then regenerate.

### Capability marketplace and MCP directory

- The MCP Connector Directory is a repository- or operator-maintained catalog,
  not a new capability type. Imports become ordinary private `mcp`
  capabilities through `canonical.Spec`, `Store.ImportCapability`, capability
  versions, and the existing Agent binding flow.
- Catalog data lives in `catalog/mcp/catalog.json` or the trusted deployment
  override `PARSAR_MCP_CATALOG_URL`. It is validated and cached in memory; do
  not add a connector catalog table or accept catalog URLs from API requests.
- Import saves configuration only. It must not execute a command, create empty
  secrets, bind an Agent, or trust client-submitted command/args/env fields.
- Catalog provenance belongs in `capability_version.source_payload` using
  `source_format=mcp_catalog`, stable `catalog_id`, `catalog_version`, and
  `catalog_source`. Installation state uses that provenance, never a name
  comparison.

## Code quality & architecture

Parsar favors small, single-purpose files and reused helpers over growing
files and copy-pasted logic. These rules are forward-looking: they do not
require immediately splitting existing large files, but any PR that adds
substantial new code to one of the files named below as an example must
split relevant pieces out first rather than growing the file further.

### File and function size

- Go: a source file crossing ~500 lines is a signal to split by
  sub-concern before adding more code to it. New files should stay under
  this from the start.
- React/TS: a component file crossing ~400 lines is a signal to extract
  sub-components/dialogs into their own files.
- What not to imitate: `server/internal/dev/routes.go` (6300+ lines),
  `server/internal/store/store.go` (8400+ lines),
  `apps/web/src/pages/admin/AgentsPage.tsx` (2000+ lines, ~40 top-level
  functions/components in one file).

### Package and file cohesion

- A package/directory groups one domain concern. `server/internal/dev`
  currently mixes auth, capabilities, uploads, scheduled tasks, RBAC, and
  sandbox admin in one flat package — do not add another unrelated route
  group there. Give a new domain its own file at minimum, and its own
  subpackage once it needs more than ~3 files or crosses ~800 lines.
- Store methods belong grouped by entity, not accreted into a single
  `Store` file/struct — see `server/internal/store/store.go` as the file
  not to imitate.

### No duplicate logic

- Before writing a formatter, parser, validator, or error-mapping helper,
  grep for an existing one. Reuse or extend it rather than writing a
  second `formatDuration` / `parseXID` / `writeXError`.
- If the same 3+ line pattern appears at a second call site, extract it
  before a third copy is added.
- Known helpers to reuse rather than reinvent: `decodeJSONWithField` /
  `decodeJSONWithFields` (`server/internal/dev/routes.go`) for JSON body
  decode errors; `parseLimit` / `parseOffset` (same file) for pagination;
  `apiRequest<T>()` (`apps/web/src/lib/api-client.ts`) for all HTTP calls
  from the web app — do not hand-roll `fetch`.

### Error-handling contract (Go handlers)

- One error-response helper per API surface, not a new sentinel→HTTP-status
  switch per file. `server/internal/dev/` currently has 6+ near-duplicate
  mappers (`writeRBACError`, `writeCredentialKindError`,
  `writeCapabilityError`, `writeImportParseError`, `writeReadError`,
  `writeStoreAgentError`) — new handlers must reuse an existing mapper for
  their domain instead of writing a parallel one, and must not inline an
  ad hoc `switch { case errors.Is(...) }` in the handler body.

### Frontend shared logic

- Cross-page utilities (date/time/duration formatting, status labels,
  etc.) live once in `apps/web/src/lib/`. Do not reimplement inside a page
  component "because it's just a few lines" — that is how
  `RunsPage.tsx`'s `fmtDuration` and `AgentsPage.tsx`'s `durationMs` /
  `formatDurationMs` diverged into two slightly different
  implementations.
- `packages/ui` / `packages/core` are reserved for logic shared across
  more than one app. Until they are populated, shared web-only logic
  still belongs in `apps/web/src/lib/`, not duplicated per page.
- Global theme state lives in `apps/web/src/lib/theme.tsx`; page components
  must not read or write theme `localStorage` directly. Light/dark styling
  must flow through semantic tokens in `apps/web/src/style.css`, not per-page
  raw colors or duplicated `dark:` branches.

### Testing granularity

- When a function or file is split for the reasons above, its test moves
  or splits with it. Do not keep appending to an already-large `_test.go`
  (e.g. `routes_test.go`, `store_test.go`) for newly extracted code — give
  the new file its own scoped test file.

## Web UI hard rules

Dialogs / drawers / modals and detail panels **must not show a horizontal
scrollbar**. End users report "I can't see the bottom" far more often than
"my screen is too narrow", and horizontal scroll almost always means a
layout bug has leaked through — it is rarely intentional design.

Three concrete rules:

1. `DialogContent` defaults to `overflow-x-hidden`. Vertical overflow uses
   `max-h-[calc(100vh-2rem)] overflow-y-auto`.
2. Every `<pre>` / `<code>` block defaults to `whitespace-pre-wrap
   break-all` — code / JSON / shell commands should wrap, not force the
   user to scroll sideways. Exception: append-only terminal log streams may
   keep `overflow-x-auto`, but only when nested inside an
   `overflow-hidden` parent so the scrollbar can't escape the dialog.
3. Error / warning banners always carry `break-all` so long tokens, URLs,
   or stack traces can't blow the container open.

When a dialog uses a multi-column grid, give every column `min-w-0` —
otherwise long children push the grid track wider instead of wrapping.

## Typography contract

The type scale has 7 defined steps. Arbitrary pixel sizes (`text-[Npx]`) are
banned by ESLint and will fail `make check`.

| Utility     | Size   | Usage                                    |
|-------------|--------|------------------------------------------|
| `text-xs`   | 12 px  | Badges, micro-meta, table footnotes      |
| `text-sm`   | 13 px  | Default body in dense admin UI           |
| `text-base` | 14 px  | Form labels, buttons, inputs             |
| `text-lg`   | 16 px  | Card titles, dialog headings             |
| `text-xl`   | 20 px  | Section headings, sub-page titles        |
| `text-2xl`  | 22 px  | Secondary page headings, feature names   |
| `text-3xl`  | 28 px  | Page titles (display, with font-display) |

### Heading hierarchy

- **h1** — Page title only. `font-display text-3xl font-semibold leading-tight
  tracking-tight text-fg`. Rendered in Space Grotesk. One per page, always
  inside `<PageHeader>`.
- **h2** — Section heading. `text-xl font-semibold text-fg`. Groups related
  cards or panels. Dialog titles use `text-lg font-semibold leading-none text-fg`.
- **h3** — Card/subsection title. `text-base font-semibold text-fg`.
- **h4** — Field group label. `text-sm font-medium text-fg`.

Do not use `font-display` on anything other than h1.

### Uppercase rule

`uppercase` is allowed ONLY on:

- Table column headers (`<th>`) — via the shared `TableHead` component.
- Standalone definition-term labels (`<dt>`) that label a single key-value pair.
- Single-word dividers (e.g. "OR" between auth methods).

`uppercase` is BANNED on:

- Any heading element (h1–h4).
- Form field labels.
- Section group labels.
- Navigation items.

If in doubt, do not uppercase. The monochrome palette + font-weight alone
provides sufficient hierarchy without case transformation.

### Raw palette ban

Never use Tailwind's built-in color palette directly (e.g. `text-slate-500`,
`bg-red-50`). All colors must go through semantic tokens defined in
`src/style.css` `@theme` block (`text-fg`, `text-fg-muted`, `bg-surface`,
`border-line`, `text-danger`, etc.). ESLint enforces this.

## Code comments

Write no comments by default. Leave a single line in the source only when
**the WHY is non-obvious**: hidden constraints, invariants, a workaround
for a specific bug, behaviour a reader would not expect. Otherwise, none.

- Don't explain WHAT — identifiers should carry that. If deleting the
  comment doesn't hurt comprehension, don't write it.
- Don't write long docstrings or multi-line block comments.
- Don't stamp the current task, caller, PR/MR, or issue number — those
  belong in the commit message and PR description; in source they rot
  during refactors.
- For exported Go symbols that need a doc comment, keep it to a single
  line — don't expand into paragraphs.

## Required checks

Before reporting completion, you must run:

```bash
make check
```

`make check` is the full local gate. It is composed of narrower targets that
CI may run independently based on the changed paths: `make check-go` for sqlc
drift plus non-store Go tests, `make check-store` for migration/store
integration tests, `make check-web` for web typecheck plus design lint, and
`make check-cli` for CLI/plugin typechecks. Keep the subtargets aligned with
the full gate whenever the required checks change.

- Any DB change must ship with a migration. Migrations are immutable
  the moment they land on `main` — prod has already applied them, so
  editing an existing file only mutates fresh installs. To change
  schema, add a **new** migration numbered strictly above the current
  head; CI (`.github/workflows/migrations.yml`) rejects edits to
  landed files and numeric regressions.
- After editing `server/internal/db/queries/*.sql`, run
  `make sqlc-generate` and commit the regenerated
  `server/internal/db/sqlc/*.go` alongside the SQL. `make check` reruns
  the generator in CI and fails the build on any drift.
- API contracts live on the handler: every `http.HandlerFunc` factory
  must carry a swaggo annotation block (`@Summary`, `@Tags`, `@Param`,
  `@Success/@Failure`, `@Router`) directly above the `func`. After
  changing a handler or its annotations, run `make openapi` to
  regenerate `docs/openapi/openapi.yaml` and commit the diff alongside
  the code. Do NOT edit the YAML by hand — CI regenerates it and fails
  the build on any drift. See `server/internal/api/health.go:livenessHandler`
  and `server/internal/dev/routes.go:listWorkspaceEnabledAgents` for
  the reference style.
- Use `internal/obs/log` for all logging — never `slog.Default()`,
  `log.Println`, `fmt.Println`, or a hand-rolled `*slog.Logger`. The
  linter (`forbidigo`) rejects direct `slog.Default()` outside
  `internal/obs/log` itself. Entry points:
  - `log.Info(ctx, ...)` / `log.Warn(ctx, ...)` / `log.Error(ctx, ...)`
    for request-scoped logs (routes through ContextHandler → trace_id
    attribution).
  - `log.Bg()` for ctx-less startup / shutdown / init code that runs
    outside a request.
  - `log.With("component", "foo")` when you need a scoped `*slog.Logger`
    to hold on to (e.g. inside a handler struct).

## Local CI parity

Before pushing, run these locally so you don't burn a round-trip on
GitHub Actions:

```
make check                    # full required repository gate
make check-go                 # sqlc drift + non-store Go tests
make check-store              # migration + store integration tests
make check-web                # web typecheck + design lint
make check-cli                # CLI/plugin typechecks
make openapi                  # regenerate docs/openapi/openapi.yaml
make sqlc-generate            # regenerate internal/db/sqlc/*.go
cd apps/web && pnpm typecheck # TS type-check web
```

If `make openapi` or `make sqlc-generate` produced a diff, commit it
alongside the source change. CI reruns both generators and fails on
any drift.

**sqlc pinned to v1.29.0.** v1.30+ declares `go >= 1.26` in its
go.mod, which would force `go run` to fetch a newer toolchain than
this repo builds under (go 1.25.12). If you bump sqlc, update
`SQLC_VERSION` in both `Makefile` and `.github/workflows/check.yml` in
the same commit. CI caches a small sqlc binary for `make check-go` and
passes it via the `SQLC` make override; local development defaults to
`go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)`.

## Report language

Verification reports and delivery reports default to English.

Except for user-facing internationalized bilingual copy, comments and
documentation must be written in English.
