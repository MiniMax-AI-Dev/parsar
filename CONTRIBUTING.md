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

- The one-command installer is both install and upgrade path. Default GHCR
  images must be pulled before `docker compose up` so `:latest` does not
  silently reuse a stale local image after `main` changes.
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
- A Parsar conversation is not a long-lived OS process. Each prompt turn may
  start a CLI process, but the adapter must either resume the upstream engine
  session on the next turn or explicitly document why that engine cannot
  resume.
- When an engine supports resume, persist the upstream session id through
  `agent_engine_sessions` and pass `AgentSessionID` plus `AgentStateKey` over
  the daemon protocol. Do not keep resume ids only in adapter memory, files
  without a server record, or frontend state.
- Adapter-specific state directories must be derived from `AgentStateKey`
  under `~/.parsar/`; never use the repo checkout, container image working
  directory, or the process CWD as hidden state.

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
- Generated files (`docs/openapi/openapi.yaml`, `server/internal/db/sqlc/*`)
  are committed artifacts, but never the source of truth. Change annotations
  or SQL first, then regenerate.

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

- Any DB change must ship with a migration. Migrations are immutable
  the moment they land on `main` — prod has already applied them, so
  editing an existing file only mutates fresh installs. To change
  schema, add a **new** migration numbered strictly above the current
  head; CI (`.github/workflows/migrations.yml`) rejects edits to
  landed files and numeric regressions.
- After editing `server/internal/db/queries/*.sql`, run
  `make sqlc-generate` and commit the regenerated
  `server/internal/db/sqlc/*.go` alongside the SQL. CI
  (`.github/workflows/sqlc.yml`) reruns the generator and fails the
  build on any drift.
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
make check                    # go vet + go test ./...
make openapi                  # regenerate docs/openapi/openapi.yaml
make sqlc-generate            # regenerate internal/db/sqlc/*.go
cd apps/web && pnpm typecheck # TS type-check web
```

If `make openapi` or `make sqlc-generate` produced a diff, commit it
alongside the source change. CI reruns both generators and fails on
any drift.

**sqlc pinned to v1.29.0.** v1.30+ declares `go >= 1.26` in its
go.mod, which would force `go run` to fetch a newer toolchain than
this repo builds under (go 1.25.12). If you bump sqlc, update **both**
`Makefile:sqlc-generate` AND `scripts/check.sh` in the same commit —
CI runs the latter, dev loops run the former, and mismatch produces
drift that only shows up on the runner.

## Report language

Verification reports and delivery reports default to English.

Except for user-facing internationalized bilingual copy, comments and
documentation must be written in English.
