# Parsar architecture baseline

Parsar is an open-source Agent collaboration control plane for teams.

```text
Parsar = team collaboration control plane + Agent Connector layer
```

> Engineering rules (worktrees, mandatory checks, directory hygiene, etc.) live
> in [`AGENTS.md`](../AGENTS.md); this document only records the **architectural
> boundaries**: protocols, connectors, and toolchain choices.

## Tech stack

- Server: Go + Chi.
- Database: PostgreSQL only.
- Web: Vite + React SPA.
- Agent runtime: `parsar-daemon` (Go), paired with a user device or with a
  platform-hosted sandbox.
- API: OpenAPI-first (contract in [`openapi/openapi.yaml`](openapi/openapi.yaml)).
- Deployment: self-host first; the default Docker Compose = Parsar + Postgres.
- DB toolchain: goose manages migrations, sqlc generates typed queries from
  checked-in SQL, pgx / pgxpool are the runtime connection and execution layer.

Parsar does not use GORM or any other ORM on the core server path. SQL is part
of the review contract: migrations define the schema, query files define the
data-access surface, and generated Go keeps call sites typed.

Reuse mature external capabilities where possible. Parsar owns the
collaboration control plane, permission model, Agent orchestration records,
and connector boundary; we do not reinvent migration engines, ORMs, browser
test frameworks, or runtime tooling.

## Agent execution entry paths

- **Agent Daemon Connector** (`connector_type=agent_daemon`) — the unified path
  for CLI Agents today. The adapter inside `parsar-daemon` is chosen by
  `project_agents.config.agent_kind` (`opencode`, `claude_code`, and any future
  kinds).
- **HTTP Agent Connector** — for Agents that expose their own HTTP interface
  and are claimed by the HTTP runner.

Runs on the Agent Daemon path are dispatched over a streaming WebSocket to the
**explicitly bound** runtime (`project_agents.runtime_id`); the default path
**no longer** falls back to acquiring a sandbox on demand.

## Protocol boundaries

```text
Server ↔ parsar-daemon:
  Agent Daemon WebSocket (pairing → heartbeat → run dispatch)

Agent Connector layer:
  Agent Daemon Connector  (opencode / claude_code / future adapters)
  HTTP Agent Connector
  ACP Connector (planned)
  A2A Connector (planned)

Agent runtime ↔ tools:
  MCP
```

## DB toolchain boundaries

- `goose` owns migration ordering and schema versioning. `server/cmd/migrate`
  is a thin wrapper around goose that keeps scripts on one stable command.
- `sqlc` compiles the checked-in SQL under `server/internal/db/queries/` into
  typed Go methods under `server/internal/db/sqlc/`.
- `pgx` / `pgxpool` are the application's connection and execution boundary.
  `database/sql` appears only at the goose wrapper boundary, because goose
  operates on a `*sql.DB`.

## Product verification boundary

Product E2E uses **Playwright** as the core quality gate — it validates
Parsar's own web / API behaviour from the user's point of view. Browser
automation *inside* an Agent runtime is a separate concern: tools like
`browser-use` may later be evaluated as a browser capability for Agents, but
they are **not** part of the current core quality gate.
