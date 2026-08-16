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
- **Pluggable runtimes.** Claude Code today, Codex tomorrow, your in-house agent next.
- **Pluggable surfaces.** Feishu / Lark ships today; Slack, Discord, and webhooks on the roadmap.
- **Auditable.** Every run is persisted: prompt, diff, logs, exit code.
- **Self-hosted.** Your code, your secrets, your machine. No telemetry.

## Quick Start

Install Parsar in one command — see [`INSTALL.md`](./INSTALL.md) for the full
guide, upgrade steps, and configuration knobs.

```bash
curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh | bash
```

Open <http://127.0.0.1:18080> and create the first owner account in the web
setup flow. The first registered user is the administrator.

## Contributing

- Operator / install / upgrade: see [`INSTALL.md`](./INSTALL.md).
- Architecture, tech stack, development setup, and coding conventions: see
  [`CONTRIBUTING.md`](./CONTRIBUTING.md). The dev workflow lives under
  `make help`.

## Security

Found a vulnerability? Please file a private report via [GitHub Security Advisories](https://github.com/MiniMax-AI-Dev/parsar/security/advisories/new) — see [`SECURITY.md`](SECURITY.md) for the full policy.

## License

[MIT](LICENSE) — 100% open source, no "open core" split.
