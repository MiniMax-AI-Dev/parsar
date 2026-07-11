# INSTALL - Parsar Local Quickstart

## One-command install

Parsar can be installed without cloning the repository. The installer writes
all generated config, secrets, database files, and runtime state under
`~/.parsar/`.

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
- Linux host with access to `/var/run/docker.sock`. The local compose stack
  enables Docker-managed agent sandboxes and mounts the Docker socket.
- To actually run Agents in sandbox mode, build the sandbox image first —
  there is no published default:
  ```bash
  docker build -f infra/sandbox/Dockerfile.local -t parsar-sandbox:local .
  ```
  Everything else in the stack works without it.

## What The Installer Does

The script:

- uses `docker-compose.yml` from the current checkout, or downloads the
  published copy to `~/.parsar/docker-compose.yml`
- creates `~/.parsar/.env`
- generates and persists `PARSAR_MASTER_KEY`
- generates and persists the Postgres password
- starts Postgres, runs migrations, and starts `parsar-server`
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

The installer preserves generated secrets and refreshes non-secret settings
such as ports, image names, and paths.
