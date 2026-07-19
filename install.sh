#!/usr/bin/env bash
set -euo pipefail

COMPOSE_URL="https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/docker-compose.yml"

usage() {
  cat <<'USAGE'
Install or upgrade Parsar with Docker Compose.

Usage:
  ./install.sh [options]
  curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh | bash

Modes:
  Default                   Install or upgrade the stack at ~/.parsar.
  --status                  Report current stack status and exit (no changes).
  --restart                 Stop, then start, the stack. Secrets and data
                            under ~/.parsar/ are preserved.
  --dry-run                 Prepare and validate without starting containers.

Options:
  --home PATH               Install directory. Default: ~/.parsar
  --port PORT               Web UI port. Default: 18080
  --bind ADDRESS            Web UI bind address. Default: 127.0.0.1
  --image IMAGE             Override the server image.
  --sandbox-image IMAGE     Override the sandbox image.
  --compose-file PATH       Use an existing Compose file.
  --no-sandbox              Skip the parsar-runtime service (Linux-only sandbox
                            container). Postgres and parsar-server still come
                            up; the admin UI marks runtimes as "unscheduled"
                            until you provide a real runtime.
  --help                    Show this help.

Advanced Compose settings can be added directly to ~/.parsar/.env.
USAGE
}

log() {
  printf '[parsar-install] %s\n' "$*"
}

die() {
  printf '[parsar-install] ERROR: %s\n' "$*" >&2
  exit 1
}

expand_path() {
  case "$1" in
    "~") printf '%s\n' "$HOME" ;;
    "~/"*) printf '%s/%s\n' "$HOME" "${1#~/}" ;;
    /*) printf '%s\n' "$1" ;;
    *) die "path must be absolute or start with ~/: $1" ;;
  esac
}

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$1"
  else
    od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'
  fi
}

ensure_env() {
  local key="$1" value="$2"
  grep -q "^${key}=" "$env_file" 2>/dev/null || printf '%s=%s\n' "$key" "$value" >>"$env_file"
}

set_env() {
  local key="$1" value="$2" temp_file="${env_file}.tmp.$$"
  grep -v "^${key}=" "$env_file" 2>/dev/null >"$temp_file" || true
  printf '%s=%s\n' "$key" "$value" >>"$temp_file"
  mv "$temp_file" "$env_file"
}

detect_docker() {
  if docker compose version >/dev/null 2>&1 </dev/null && docker info >/dev/null 2>&1 </dev/null; then
    DOCKER=(docker)
  elif command -v sudo >/dev/null 2>&1 &&
    sudo -n docker compose version >/dev/null 2>&1 </dev/null &&
    sudo -n docker info >/dev/null 2>&1 </dev/null; then
    DOCKER=(sudo -n docker)
  else
    die "Docker Compose v2 is required and the current user cannot reach Docker"
  fi
}

docker_run() {
  "${DOCKER[@]}" "$@" </dev/null
}

compose() {
  docker_run compose -f "$compose_file" --env-file "$env_file" "$@"
}

download_compose() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$COMPOSE_URL" -o "$compose_file"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$compose_file" "$COMPOSE_URL"
  else
    die "curl or wget is required"
  fi
}

wait_for_health() {
  for _ in $(seq 1 60); do
    if compose exec -T parsar-server wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

home="${PARSAR_HOME:-$HOME/.parsar}"
port="${PARSAR_LOCAL_PORT:-18080}"
bind="${PARSAR_BIND_ADDR:-127.0.0.1}"
server_image="${PARSAR_SERVER_IMAGE:-}"
sandbox_image="${PARSAR_SANDBOX_IMAGE:-}"
compose_source="${PARSAR_COMPOSE_FILE:-}"
dry_run=false
mode="install"
no_sandbox=false

while [ "$#" -gt 0 ]; do
  case "$1" in
    --home) home="${2:?missing value for --home}"; shift 2 ;;
    --port) port="${2:?missing value for --port}"; shift 2 ;;
    --bind) bind="${2:?missing value for --bind}"; shift 2 ;;
    --image) server_image="${2:?missing value for --image}"; shift 2 ;;
    --sandbox-image) sandbox_image="${2:?missing value for --sandbox-image}"; shift 2 ;;
    --compose-file) compose_source="${2:?missing value for --compose-file}"; shift 2 ;;
    --dry-run) dry_run=true; shift ;;
    --status) mode="status"; shift ;;
    --restart) mode="restart"; shift ;;
    --no-sandbox) no_sandbox=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

case "$port" in (*[!0-9]*|"") die "--port must be numeric" ;; esac

home="$(expand_path "$home")"
mkdir -p "$home/postgres" "$home/data"
umask 077
env_file="$home/.env"
touch "$env_file"

# --status / --restart require an existing install.
if [[ "$mode" != "install" && ! -f "$env_file" ]]; then
  die "--${mode} requires an existing install at $home (no .env found). Run install.sh once first."
fi

if [ -n "$compose_source" ]; then
  compose_file="$(expand_path "$compose_source")"
  [ -f "$compose_file" ] || die "compose file does not exist: $compose_file"
elif [ -f "$PWD/docker-compose.yml" ]; then
  compose_file="$PWD/docker-compose.yml"
else
  compose_file="$home/docker-compose.yml"
  # Skip re-downloading on --status when an existing file is present,
  # so a checkout that ships a newer docker-compose.yml is honored as-is.
  if [[ "$mode" == "status" && -f "$compose_file" ]]; then
    :
  else
    download_compose
  fi
fi

set_env PARSAR_LOCAL_PORT "$port"
set_env PARSAR_BIND_ADDR "$bind"
set_env PARSAR_PG_DATA_DIR "$home/postgres"
set_env PARSAR_DATA_DIR "$home/data"
if [ "$no_sandbox" = true ]; then
  ensure_env PARSAR_COMPOSE_PROFILES "no_sandbox"
fi
[ -z "$server_image" ] || set_env PARSAR_SERVER_IMAGE "$server_image"
[ -z "$sandbox_image" ] || set_env PARSAR_SANDBOX_IMAGE "$sandbox_image"
ensure_env PARSAR_PG_PASSWORD "$(random_hex 24)"
ensure_env PARSAR_MASTER_KEY "$(random_hex 32)"
ensure_env PARSAR_SHARED_RUNTIME_TOKEN "$(random_hex 32)"

detect_docker
log "Using $compose_file"
log "Using $env_file"

# --status exits before pulling images or starting anything.
if [[ "$mode" == "status" ]]; then
  compose config --quiet
  compose ps || true
  cat <<STATUS

Run `./install.sh --restart` to recycle the stack.
Run `./install.sh` (default) to upgrade images in place.
STATUS
  exit 0
fi

compose config --quiet

if [ "$dry_run" = true ]; then
  log "Dry run complete"
  exit 0
fi

pull_targets=(parsar-server)
if [[ "$no_sandbox" != true ]]; then
  pull_targets+=(parsar-runtime)
fi

log "Pulling images: ${pull_targets[*]}"
# shellcheck disable=SC2068
compose pull ${pull_targets[@]}

if [[ "$mode" == "restart" ]]; then
  log "Stopping stack"
  compose down --remove-orphans
fi

profile_args=()
if [[ "$no_sandbox" == true ]]; then
  profile_args+=(--profile no_sandbox)
  log "Sandbox service is disabled (--no-sandbox)."
fi

log "Starting Parsar"
# shellcheck disable=SC2068
compose up -d --remove-orphans ${profile_args[@]}

if ! wait_for_health; then
  compose ps >&2 || true
  compose logs --tail 120 parsar-server >&2 || true
  die "parsar-server did not become healthy"
fi

cat <<EOF

Parsar is running at http://${bind}:${port}

Create the first owner account in the web setup flow.

Manage the stack:
  ./install.sh --status
  ./install.sh --restart
  ${DOCKER[*]} compose -f "$compose_file" --env-file "$env_file" logs -f

EOF
