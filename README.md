# Parsar

Parsar is an open-source Agent collaboration control plane for teams —
it lets a Feishu (Lark) group chat dispatch coding tasks to background
agents (Claude Code, Codex, etc.), tracks their runs, and surfaces
results back into the chat.

## Pick your path

| You want to | Read this | Time |
|---|---|---|
| **Try it on my laptop** (mock auth, docker compose) | [`INSTALL.md`](INSTALL.md) | ~5 min wall-clock, mostly Docker build |
| **Hack on the code** (hot-reload server + web) | [Local development](#local-development) below | ~10 min first-time setup |
| **Self-host for a real team** (Feishu OAuth, persistent host) | [`docs/deploy/deploy-runbook.md`](docs/deploy/deploy-runbook.md) | 30–60 min |

`INSTALL.md` is written for an AI coding agent (Claude Code, Cursor,
Codex) to follow step-by-step from a fresh clone. If you have one of
those, just open it in the repo and say *"read INSTALL.md and get this
running"*.

## Stack

- Server: Go + Chi.
- Database: PostgreSQL only.
- DB tooling: goose migrations + sqlc generated queries + pgx/pgxpool.
- Web: Vite + React SPA.
- API: OpenAPI-first (`docs/openapi/openapi.yaml`).

## Local development

For contributors changing server / web code (not for evaluation —
evaluators should use `INSTALL.md` above).

```bash
make setup        # bootstrap deps (Go, pnpm, Postgres container)
make dev          # run server + web + Postgres (hot reload)
make check        # required before reporting completion
```

Prerequisites: Go ≥ 1.22, Node ≥ 20, pnpm ≥ 9, Docker (for Postgres).

## Runtime path rule

Parsar must not write config, logs, state, or cache to the current
working directory. All local runtime data lives under:

```text
~/.parsar/
```

See [`docs/runtime-paths.md`](docs/runtime-paths.md).

## Development rules

Long-term development rules — worktree workflow, architecture baseline,
required checks — live in [`AGENTS.md`](AGENTS.md). Read it before any
implementation, refactor, or schema/API change.
