

<h3 align="center">Parsar</h3>

<p align="center">
  <b>Your team's intent, parsed into action.</b>
  <br />
  The open-source agent collaboration control plane for engineering teams.
  <br />
  <a href="docs/"><strong>Docs »</strong></a>
  <br />
  <br />
  <a href="#quick-start"><strong>Quick Start</strong></a> ·
  <a href="#tech-stack"><strong>Tech Stack</strong></a> ·
  <a href="#self-hosting"><strong>Self-Hosting</strong></a> ·
  <a href="#contributing"><strong>Contributing</strong></a>
</p>


## Introduction

**Parsar** is where engineering teams collaborate with AI coding agents.

Dispatch tasks to background agents — **Claude Code**, **Codex**, and others — from the tools your team already lives in. Every run is sandboxed, tracked end-to-end, and the result flows back to where it started: a chat thread, a ticket, a PR, a webhook.

It is the connective tissue between **where work is discussed** and **where work gets done** — without anyone leaving their workflow.

<p align="center">
  <img src="docs/assets/demo.png" alt="Parsar dispatching a task to an agent" width="720">
</p>

### Why Parsar

- **Team-first.** Built around shared queues, run history, and permissions — not single-player agent loops.
- **Pluggable agents.** Claude Code today, Codex tomorrow, your in-house agent next week. Workers are subprocesses behind a contract.
- **Pluggable surfaces.** Feishu / Lark ships in-box today; Slack, Discord, web UI, and a raw HTTP API are first-class extension points.
- **Auditable.** Every run is persisted: prompt, diff, logs, exit code. PostgreSQL is the only source of truth.
- **Self-hosted by design.** Your code, your secrets, your machine. No telemetry, no phone-home.
- **OpenAPI-first.** Schema lives in [`docs/openapi/openapi.yaml`](docs/openapi/openapi.yaml); server handlers and TS client are both generated from it.

## Architecture

```
   Team surface   ──▶   Parsar server   ──▶   Agent worker (sandboxed)
   (chat / web /         │                          │
    API / webhook)       │                          │
        ▲                │                          │
        └──── results ───┴──── PostgreSQL ◀─────────┘
```

A single Go binary, a single Postgres database, and one worker process per agent type. No Redis, no message queue, no Kafka — Postgres carries the queue, the state, and the audit log. Surfaces (chat providers, web UI, raw API) all talk to the same server through OpenAPI. See [`docs/architecture.md`](docs/architecture.md) for the long version.

## Tech Stack

|              | Choice                                                                                                       |
|--------------|--------------------------------------------------------------------------------------------------------------|
| **Server**   | [Go 1.22](https://go.dev) + [Chi](https://github.com/go-chi/chi)                                             |
| **Database** | [PostgreSQL 16](https://www.postgresql.org) — single source of truth                                         |
| **DB layer** | [goose](https://github.com/pressly/goose) migrations · [sqlc](https://sqlc.dev) queries · [pgx](https://github.com/jackc/pgx) pool |
| **Web**      | [Vite](https://vitejs.dev) + [React 18](https://react.dev) SPA                                               |
| **API**      | [OpenAPI 3](https://www.openapis.org) — server & client generated                                            |
| **Agents**   | [Claude Code](https://claude.ai/code) · [Codex](https://openai.com/codex) (pluggable)                        |
| **Surfaces** | Feishu / Lark · web UI · HTTP API · webhooks (Slack & Discord on the roadmap)                                |
| **Monorepo** | [Turborepo](https://turbo.build) + [pnpm](https://pnpm.io) workspaces                                        |

## Quick Start

Three paths — pick what matches your goal.

| Goal | Read | Time |
|------|------|------|
| **Try it on my laptop** (mock auth, Docker Compose) | [`INSTALL.md`](INSTALL.md) | ~5 min |
| **Hack on the code** (hot-reload server + web) | [Development](#development) below | ~10 min |
| **Self-host for a real team** (real auth, persistent host, your chat provider) | [`docs/deploy/deploy-runbook.md`](docs/deploy/deploy-runbook.md) | 30–60 min |

> [!TIP]
> `INSTALL.md` is written for an **AI coding agent** to follow from a fresh clone. Open Cursor / Claude Code in the repo and say *"read INSTALL.md and get this running"* — it will.

## Development

**Prerequisites:** Go ≥ 1.22 · Node ≥ 20 · pnpm ≥ 9 · Docker (for Postgres)

```bash
make setup        # bootstrap deps and start a Postgres container
make dev          # run server + web + Postgres with hot reload
make check        # lint, generate, test — required before any PR
```

Open <http://localhost:5173> for the web UI, <http://localhost:8080> for the server.

### Runtime paths

Parsar **never** writes to the current working directory. All runtime state — config, logs, sqlite cache, worker scratch space — lives under `~/.parsar/`. See [`docs/runtime-paths.md`](docs/runtime-paths.md).

### Project layout

```
apps/       Go server, React web, agent workers
packages/   shared TS types (generated from OpenAPI)
internal/   private Go packages
docs/       architecture, runbooks, OpenAPI spec
deploy/     Helm chart, systemd units, sample Compose files
```

## Self-Hosting

Production deploy is a single Docker Compose file plus credentials for whichever surface(s) your team uses. Follow [`docs/deploy/deploy-runbook.md`](docs/deploy/deploy-runbook.md) end-to-end — it covers reverse proxy, TLS, secrets, backup, and upgrade.

For a one-host evaluation deploy:

```bash
cp .env.example .env   # set agent credentials, surface credentials, POSTGRES_PASSWORD
docker compose -f docker-compose.local.yml up -d
```

## Security

Found a vulnerability? Please email **security@your-org.com** instead of opening a public issue.

## License

Parsar is released under the [MIT License](LICENSE). 100% open source, no "open core" split, no enterprise-only features.
