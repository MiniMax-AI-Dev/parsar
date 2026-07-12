# INSTALL - Parsar Local Quickstart

## One-command install

Parsar can be installed without cloning the repository. The installer is a
small wrapper around the root `docker-compose.yml`; it keeps generated config,
secrets, database files, and runtime state under `~/.parsar/`.

```bash
curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh | bash
```

When it finishes, open:

```text
http://127.0.0.1:18080
```

Create the first owner account in the web setup flow. The first registered
user is the administrator.

## Requirements

- Docker Engine with Docker Compose v2.
- No host Docker socket mount is required for the default install. The local
  runtime is a compose service that runs the sandbox image
  (`ghcr.io/minimax-ai-dev/parsar-sandbox:latest`) and connects back with
  `parsar-daemon`. Build your own image instead with:
  ```bash
  docker build -f infra/sandbox/Dockerfile -t parsar-sandbox:local .
  PARSAR_SANDBOX_IMAGE=parsar-sandbox:local ./install.sh
  ```

## What The Installer Does

The script:

- uses `docker-compose.yml` from the current checkout, or downloads the
  published copy to `~/.parsar/docker-compose.yml`
- creates `~/.parsar/.env` with persistent random secrets and data paths
- pulls the Parsar images so upgrades do not reuse a stale `latest` image
- starts Postgres, `parsar-server`, and the default local runtime service
- waits for the server health check
- leaves Feishu, Slack, and Discord integrations disabled unless explicitly
  enabled in `~/.parsar/.env`

It does not write runtime state into the repository or the current working
directory.

## Common Options

```bash
# Use a different web port.
curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh \
  | bash -s -- --port 18088

# Use a pinned image.
curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh \
  | bash -s -- --image ghcr.io/minimax-ai-dev/parsar-server:parsar-server-v0.1.0
```

For local development from a checkout:

```bash
make docker-build
./install.sh --image parsar:dev
```

## Manage The Stack

```bash
docker compose -f ~/.parsar/docker-compose.yml --env-file ~/.parsar/.env ps
docker compose -f ~/.parsar/docker-compose.yml --env-file ~/.parsar/.env logs -f parsar-server
docker compose -f ~/.parsar/docker-compose.yml --env-file ~/.parsar/.env down
```

To remove all local data, stop the stack first and then delete `~/.parsar/`.

## Advanced Configuration

Edit `~/.parsar/.env` and rerun:

```bash
curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh | bash
```

The installer preserves generated secrets. It intentionally exposes only the
common port, bind address, and image options; add uncommon Compose overrides
directly to `.env` instead of adding more installer flags.

When a platform such as Dokploy adds its ingress network to `parsar-server`,
keep the service attached to the Compose `default` network as well. Postgres
and the local runtime are intentionally private and use that network for
service discovery.

The Feishu WebSocket inbound manager and outbound worker are driven by the
workspace connector saved in the admin UI. No additional deployment flag is
required after enabling the connector.

Feishu, Slack, and Discord credentials are configured in the workspace admin
UI, not in `docker-compose.yml` or the installer environment.
