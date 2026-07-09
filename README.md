

<p align="center">
  <img src=".github/assets/parsar-banner.png" alt="Parsar" width="520" />
</p>

<p align="center">
  <b>Your team's intent, parsed into action.</b>
  <br />
  The open-source agent collaboration control plane for engineering teams.
</p>

> [!WARNING]
> **Alpha — under active development, not production-ready.**
> APIs, schemas, and configs may change without notice between commits.

## What is Parsar

Parsar is a team-first platform for dispatching, managing, and auditing AI coding agents. Send tasks from the tools your team already uses — chat, web UI, API — and get results back where they started: a thread, a PR, a webhook.

Supported agent runtimes:

- **Claude Code**
- **Piagent**
- **Codex**
- More to come — the runtime layer is pluggable.

### Why Parsar

- **Team-first.** Shared queues, run history, and permissions — not single-player agent loops.
- **Pluggable runtimes.** Claude Code today, Codex tomorrow, your in-house agent next week.
- **Pluggable surfaces.** Feishu / Lark ships today; Slack, Discord, and webhooks on the roadmap.
- **Auditable.** Every run is persisted: prompt, diff, logs, exit code.
- **Self-hosted.** Your code, your secrets, your machine. No telemetry.

## Quick Start

Requires only `git` and `docker`.

```bash
git clone https://github.com/MiniMax-AI-Dev/parsar.git
cd parsar
docker build -t parsar:local .
PARSAR_SERVER_IMAGE=parsar:local docker compose -f docker-compose.local.yml up
```

Open <http://127.0.0.1:18080>. Mock auth signs you in as `admin@example.com` — no secrets, no `.env`, no config.

> **Platform.** Verified on Linux/amd64. The Agent sandbox image is currently amd64-only, so Apple Silicon (arm64) is not yet supported out of the box.

## Contributing

Architecture, tech stack, development setup, and coding conventions are all in [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

Found a vulnerability? Please file a private report via [GitHub Security Advisories](https://github.com/MiniMax-AI-Dev/parsar/security/advisories/new) — see [`SECURITY.md`](SECURITY.md) for the full policy.

## License

[MIT](LICENSE) — 100% open source, no "open core" split.
