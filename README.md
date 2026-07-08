

<p align="center">
  <img src=".github/assets/parsar-banner.png" alt="Parsar" width="520" />
</p>

<p align="center">
  <b>Your team's intent, parsed into action.</b>
  <br />
  <br />
  ⚠️ <strong>Alpha — under active development, not production-ready.</strong>
</p>

## What is Parsar

Parsar is a team-first AI agent collaboration platform. It lets engineering teams dispatch, manage, and audit coding tasks across multiple agent runtimes from one control plane.

Supported runtimes:

- **Claude Code**
- **Piagent**
- **Codex**
- More to come — the runtime layer is pluggable.

## Quick Start

```bash
git clone https://github.com/MiniMax-AI-Dev/parsar.git
cd parsar
docker build -t parsar:local .
PARSAR_SERVER_IMAGE=parsar:local docker compose -f docker-compose.local.yml up
```

Open <http://127.0.0.1:18080>.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for architecture, tech stack, development setup, and coding conventions.

## License

[MIT](LICENSE)
