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
