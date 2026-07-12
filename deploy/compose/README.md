# `deploy/compose/` — self-hosted deployment templates

This directory contains the Docker Compose template and example environment
file for self-hosted Parsar deployments. For setup, secrets, migrations,
bootstrap, health checks, and production operations, follow the canonical
[`deploy runbook`](../../docs/deploy/deploy-runbook.md).

## Files

| File | Purpose |
|---|---|
| [`compose.selfhost.yml`](./compose.selfhost.yml) | Self-hosted Postgres and Parsar server services. |
| [`.env.example`](./.env.example) | Placeholder values for the Compose and server environment. Do not store real secrets here. |

## Validate

Copy `.env.example` to an untracked `.env`, fill its required placeholders,
then verify the resolved Compose configuration without starting containers:

```bash
docker compose -f deploy/compose/compose.selfhost.yml \
  --env-file deploy/compose/.env config
```
