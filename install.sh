#!/usr/bin/env bash
set -euo pipefail

DEFAULT_COMPOSE_URL="https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/docker-compose.yml"
DEFAULT_SERVER_IMAGE="ghcr.io/minimax-ai-dev/parsar-server:latest"
DEFAULT_SANDBOX_IMAGE="parsar-sandbox:local"

usage() {
  cat <<'EOF'
Parsar one-command installer.

Usage:
  ./install.sh [options]
  curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh | bash

Options:
  --home PATH            Install state directory. Must be absolute or ~/...
                         Default: ~/.parsar
  --compose-file PATH    Compose file to use. Default: ./docker-compose.yml
                         when present, otherwise download the published template.
  --image IMAGE          parsar-server image.
                         Default: ghcr.io/minimax-ai-dev/parsar-server:latest
  --sandbox-image IMAGE  Docker sandbox image. There is no published
                         default — build one first:
                         docker build -f infra/sandbox/Dockerfile -t parsar-sandbox:local .
                         Default: parsar-sandbox:local
  --port PORT            Web UI host port. Default: 18080
  --pg-port PORT         Postgres host port. Default: 15432
  --bind ADDR            Web UI bind address. Default: 127.0.0.1
  --public-url URL       Browser-facing URL. Default: http://127.0.0.1:<port>
  --project-name NAME    Docker Compose project name. Default: parsar
  --dry-run              Generate files and validate compose config only.
  --help                 Show this help.

Environment variables with the same names are also honored:
  PARSAR_HOME, PARSAR_COMPOSE_FILE, PARSAR_SERVER_IMAGE,
  PARSAR_SANDBOX_IMAGE, PARSAR_LOCAL_PORT, PARSAR_PG_PORT,
  PARSAR_BIND_ADDR, PARSAR_PUBLIC_URL, PARSAR_PROJECT_NAME.
EOF
}

log() {
  printf '[parsar-install] %s\n' "$*"
}

die() {
  printf '[parsar-install] ERROR: %s\n' "$*" >&2
  exit 1
}

expand_home_path() {
  case "$1" in
    "~") printf '%s\n' "$HOME" ;;
    "~/"*) printf '%s/%s\n' "$HOME" "${1#~/}" ;;
    /*) printf '%s\n' "$1" ;;
    *) die "path must be absolute or start with ~/; got: $1" ;;
  esac
}

random_hex() {
  bytes="$1"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$bytes"
    return
  fi
  od -An -N"$bytes" -tx1 /dev/urandom | tr -d ' \n'
}

ensure_env() {
  key="$1"
  value="$2"
  file="$3"
  if grep -q "^${key}=" "$file" 2>/dev/null; then
    return
  fi
  printf '%s=%s\n' "$key" "$value" >>"$file"
}

set_env() {
  key="$1"
  value="$2"
  file="$3"
  tmp="${file}.tmp.$$"
  grep -v "^${key}=" "$file" 2>/dev/null >"$tmp" || true
  printf '%s=%s\n' "$key" "$value" >>"$tmp"
  mv "$tmp" "$file"
}

detect_docker() {
  if docker compose version >/dev/null 2>&1 </dev/null && docker info >/dev/null 2>&1 </dev/null; then
    DOCKER=(docker)
    return
  fi
  if command -v sudo >/dev/null 2>&1 &&
    sudo -n docker compose version >/dev/null 2>&1 </dev/null &&
    sudo -n docker info >/dev/null 2>&1 </dev/null; then
    DOCKER=(sudo -n docker)
    return
  fi
  die "Docker Compose v2 is required and the current user cannot reach the Docker daemon"
}

# Piped via `curl | bash`, our own stdin (fd 0) is the same pipe still
# delivering the unparsed tail of this script. `docker`/`docker compose`
# subcommands like `exec` forward stdin to the container by default, and
# will happily drain that pipe out from under bash mid-parse (silently
# truncating everything after the command that stole it — including the
# closing banner). Redirect from /dev/null so no docker invocation can
# ever compete with bash for bytes of its own source.
docker_run() {
  "${DOCKER[@]}" "$@" </dev/null
}

compose_run() {
  docker_run compose -f "$compose_file" --env-file "$env_file" "$@"
}

fetch_compose() {
  if [ -n "$compose_arg" ]; then
    src="$(expand_home_path "$compose_arg")"
    [ -f "$src" ] || die "compose file does not exist: $src"
    compose_file="$src"
    return
  fi

  if [ -f "./docker-compose.yml" ]; then
    compose_file="$PWD/docker-compose.yml"
    return
  fi

  compose_file="$parsar_home/docker-compose.yml"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$DEFAULT_COMPOSE_URL" -o "$compose_file"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$compose_file" "$DEFAULT_COMPOSE_URL"
  else
    die "curl or wget is required to download docker-compose.yml"
  fi
}

wait_for_health() {
  for _ in $(seq 1 60); do
    if compose_run exec -T parsar-server wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

home_arg="${PARSAR_HOME:-$HOME/.parsar}"
compose_arg="${PARSAR_COMPOSE_FILE:-}"
server_image="${PARSAR_SERVER_IMAGE:-$DEFAULT_SERVER_IMAGE}"
sandbox_image="${PARSAR_SANDBOX_IMAGE:-$DEFAULT_SANDBOX_IMAGE}"
local_port="${PARSAR_LOCAL_PORT:-18080}"
pg_port="${PARSAR_PG_PORT:-15432}"
bind_addr="${PARSAR_BIND_ADDR:-127.0.0.1}"
public_url="${PARSAR_PUBLIC_URL:-}"
project_name="${PARSAR_PROJECT_NAME:-parsar}"
dry_run="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --home) home_arg="${2:?missing value for --home}"; shift 2 ;;
    --compose-file) compose_arg="${2:?missing value for --compose-file}"; shift 2 ;;
    --image) server_image="${2:?missing value for --image}"; shift 2 ;;
    --sandbox-image) sandbox_image="${2:?missing value for --sandbox-image}"; shift 2 ;;
    --port) local_port="${2:?missing value for --port}"; shift 2 ;;
    --pg-port) pg_port="${2:?missing value for --pg-port}"; shift 2 ;;
    --bind) bind_addr="${2:?missing value for --bind}"; shift 2 ;;
    --public-url) public_url="${2:?missing value for --public-url}"; shift 2 ;;
    --project-name) project_name="${2:?missing value for --project-name}"; shift 2 ;;
    --dry-run) dry_run="true"; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

case "$local_port" in (*[!0-9]*|"") die "--port must be numeric" ;; esac
case "$pg_port" in (*[!0-9]*|"") die "--pg-port must be numeric" ;; esac
case "$project_name" in
  ""|[!a-z0-9]*) die "--project-name must start with a lowercase letter or digit" ;;
esac
case "$project_name" in
  *[!a-z0-9_-]*) die "--project-name may contain only lowercase letters, digits, underscores, and dashes" ;;
esac

parsar_home="$(expand_home_path "$home_arg")"
if [ -z "$public_url" ]; then
  public_host="127.0.0.1"
  if [ "$bind_addr" != "127.0.0.1" ] && [ "$bind_addr" != "localhost" ]; then
    public_host="$bind_addr"
  fi
  public_url="http://${public_host}:${local_port}"
fi

detect_docker

docker_bin="$(command -v docker || true)"
docker_gid="999"
if [ -S /var/run/docker.sock ] && command -v stat >/dev/null 2>&1; then
  docker_gid="$(stat -c '%g' /var/run/docker.sock 2>/dev/null || printf '999')"
fi

umask 077
mkdir -p "$parsar_home" "$parsar_home/postgres" "$parsar_home/data"

env_file="$parsar_home/.env"
if [ ! -f "$env_file" ]; then
  : >"$env_file"
fi

fetch_compose

set_env "PARSAR_HOME" "$parsar_home" "$env_file"
set_env "PARSAR_PROJECT_NAME" "$project_name" "$env_file"
set_env "PARSAR_SERVER_IMAGE" "$server_image" "$env_file"
set_env "PARSAR_SANDBOX_IMAGE" "$sandbox_image" "$env_file"
set_env "PARSAR_LOCAL_PORT" "$local_port" "$env_file"
set_env "PARSAR_PG_PORT" "$pg_port" "$env_file"
set_env "PARSAR_BIND_ADDR" "$bind_addr" "$env_file"
set_env "PARSAR_PUBLIC_URL" "$public_url" "$env_file"
set_env "PARSAR_COOKIE_SECURE" "false" "$env_file"
ensure_env "PARSAR_PG_PASSWORD" "$(random_hex 24)" "$env_file"
ensure_env "PARSAR_MASTER_KEY" "$(random_hex 32)" "$env_file"
set_env "PARSAR_PG_DATA_DIR" "$parsar_home/postgres" "$env_file"
set_env "PARSAR_DATA_DIR" "$parsar_home/data" "$env_file"
set_env "PARSAR_DOCKER_BIN" "${docker_bin:-/usr/bin/docker}" "$env_file"
set_env "DOCKER_GID" "$docker_gid" "$env_file"

log "Using $compose_file"
log "Wrote $env_file"
compose_run config --quiet

if [ "$dry_run" = "true" ]; then
  log "Dry run complete; containers were not started"
  exit 0
fi

log "Starting Parsar with Docker Compose"
compose_run up -d --remove-orphans

if wait_for_health; then
  log "Parsar is healthy"
else
  compose_run ps >&2 || true
  compose_run logs --tail 120 parsar-server >&2 || true
  die "parsar-server did not become healthy"
fi

cat <<EOF

Parsar is running.

Open:
  ${public_url}

Create the first owner account in the web setup flow.

Useful commands:
  ${DOCKER[*]} compose -f "$compose_file" --env-file "$env_file" ps
  ${DOCKER[*]} compose -f "$compose_file" --env-file "$env_file" logs -f parsar-server
  ${DOCKER[*]} compose -f "$compose_file" --env-file "$env_file" down

State and config:
  $parsar_home

EOF
