#!/usr/bin/env bash
set -euo pipefail

COMPOSE_URL="https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/docker-compose.yml"

usage() {
  cat <<'USAGE'
Install or upgrade Parsar with Docker Compose v2.15 or newer.

Usage:
  ./install.sh [options]
  curl -fsSL https://raw.githubusercontent.com/MiniMax-AI-Dev/parsar/main/install.sh | bash

Options:
  --home PATH          Install directory. Default: ~/.parsar
  --port PORT          Web UI port. First-install default: 18080
  --bind ADDRESS       Web UI bind address. First-install default: 127.0.0.1
  --image IMAGE        Override the server image.
  --compose-file PATH  Use an existing Compose file.
  --dry-run            Validate without changing configuration or containers.
  --help               Show this help.

Existing .env settings are preserved unless explicitly overridden.
Advanced Compose settings can be added directly to <home>/.env.
Configure workspace Core bindings in <home>/core/workspaces.json after setup.
Core owns execution environments and model-provider credentials.
USAGE
}

log() { printf '[parsar-install] %s\n' "$*"; }
die() { printf '[parsar-install] ERROR: %s\n' "$*" >&2; exit 1; }

expand_path() {
  case "$1" in
    "~") printf '%s\n' "$HOME" ;;
    "~/"*) printf '%s/%s\n' "$HOME" "${1#'~/'}" ;;
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

has_env() {
  grep -Eq "^[[:space:]]*(export[[:space:]]+)?${1}[[:space:]]*[:=]" "$env_file"
}

set_env() {
  local key="$1" value="$2"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || die "$key must be a single line"
  # Escape literals before Compose performs dotenv interpolation.
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//\$/\$\$}"
  awk -v key="$key" '$0 !~ "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*[:=]"' \
    "$env_file" >"$stage/env.next"
  printf '%s="%s"\n' "$key" "$value" >>"$stage/env.next"
  mv "$stage/env.next" "$env_file"
}

ensure_env() { has_env "$1" || set_env "$1" "$2"; }

cleanup() {
  local status=$?
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    if [ "$committing" = true ] && [ "$starting" = false ]; then
      if [ -f "$stage/old.env" ]; then mv "$stage/old.env" "$final_env"; else rm -f "$final_env"; fi
      if [ "$downloaded" = true ]; then
        if [ -f "$stage/old.compose" ]; then mv "$stage/old.compose" "$final_compose"; else rm -f "$final_compose"; fi
      fi
    fi
    log "Installation stopped. Data is retained; correct the error and rerun the same command."
  fi
  [ -z "$stage" ] || rm -rf "$stage"
  if [ "$locked" = true ]; then
    rm -f "$lock_dir/pid"
    rmdir "$lock_dir"
  fi
  exit "$status"
}

begin_install() {
  umask 077
  mkdir -p "$home"
  home="$(cd "$home" && pwd -P)"
  lock_dir="$home/.install.lock"
  if ! mkdir "$lock_dir" 2>/dev/null; then
    die "installation lock exists: $lock_dir. Wait for the other installer; after a crash, verify it is no longer running before removing this lock."
  fi
  locked=true
  printf '%s\n' "$$" >"$lock_dir/pid"
  stage="$(mktemp -d "$home/.install.XXXXXX")"
}

detect_docker() {
  local version engine
  if docker compose version >/dev/null 2>&1 </dev/null && docker info >/dev/null 2>&1 </dev/null; then
    DOCKER=(docker)
  elif command -v sudo >/dev/null 2>&1 &&
    sudo -n docker compose version >/dev/null 2>&1 </dev/null &&
    sudo -n docker info >/dev/null 2>&1 </dev/null; then
    DOCKER=(sudo -n docker)
  else
    die "Docker Compose v2.15+ and a running Linux Docker engine are required. Start Docker or grant the current user access."
  fi
  version="$(docker_run compose version --short)"
  [[ "$version" =~ ^v?([0-9]+)\.([0-9]+) ]] || die "cannot determine Docker Compose version"
  ((BASH_REMATCH[1] > 2 || (BASH_REMATCH[1] == 2 && BASH_REMATCH[2] >= 15))) \
    || die "Docker Compose v2.15 or newer is required"
  engine="$(docker_run info --format '{{.OSType}}')"
  [ "$engine" = linux ] || die "Docker must be configured to run Linux containers"
}

docker_run() { "${DOCKER[@]}" "$@" </dev/null; }
compose() {
  docker_run compose --project-directory "$project_dir" -f "$compose_file" ${image_override[@]+"${image_override[@]}"} --env-file "$env_file" "$@"
}

download_compose() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 15 --max-time 120 --retry 3 "$COMPOSE_URL" -o "$compose_file"
  elif command -v wget >/dev/null 2>&1; then
    wget --timeout=30 --tries=3 -qO "$compose_file" "$COMPOSE_URL"
  else
    die "curl or wget is required"
  fi
}

wait_for_health() {
  local attempt service container state ready
  for ((attempt=0; attempt<60; attempt++)); do
    ready=true
    for service in postgres parsar-server; do
      container="$(compose ps -aq "$service")" || return 1
      [ -n "$container" ] || { ready=false; continue; }
      state="$(docker_run inspect --format '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container")" || return 1
      case "$state" in
        'running healthy'|'running none') ;;
        exited*|dead*|*unhealthy) return 1 ;;
        *) ready=false ;;
      esac
    done
    if [ "$ready" = true ] && compose exec -T parsar-server wget -T 5 -qO- http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

verify_empty_data() {
  local service target
  for service in postgres parsar-server; do
    if [ "$service" = postgres ]; then target=/var/lib/postgresql/data; else target=/var/lib/parsar; fi
    # Inspect actual mounts through Docker, including named volumes and root-owned paths.
    compose run -T --rm --no-deps --user 0:0 --workdir / --entrypoint /bin/sh "$service" \
      -ec 'entries=$(ls -A "$1") && [ -z "$entries" ]' sh "$target" \
      || die "$service data is not empty or cannot be inspected; restore the original secrets in $final_env"
  done
}

prepare_data_directory() {
  local data_owner
  data_owner="$(compose run -T --rm --no-deps --workdir / --entrypoint /bin/sh parsar-server \
    -c 'printf "%s:%s" "$(id -u)" "$(id -g)"')"
  [[ "$data_owner" =~ ^[0-9]+:[0-9]+$ ]] || die "could not determine the server image user"
  # Only preparation runs as root; the server retains its configured user.
  compose -f "$stage/prepare.yml" run -T --rm --no-deps --user 0:0 \
    --volume "$stage:/parsar-installer:ro" --workdir / --entrypoint /bin/sh parsar-server \
    -ec 'chown -Rh "$1" /var/lib/parsar
      if [ -L /run/secrets/core/workspaces.json ]; then
        echo "Core workspace configuration must not be a symlink" >&2; exit 1
      fi
      if [ ! -e /run/secrets/core/workspaces.json ]; then
        initial=$(mktemp /run/secrets/core/.workspaces.XXXXXX)
        trap '\''rm -f "$initial"'\'' EXIT
        printf "[]\n" > "$initial"
        chmod 644 "$initial"
        chown "$(stat -c %u:%g /parsar-installer)" /run/secrets/core "$initial"
        ln "$initial" /run/secrets/core/workspaces.json
      fi' sh "$data_owner"
  compose run -T --rm --no-deps --workdir / --entrypoint /bin/sh parsar-server \
    -c 'test -w /var/lib/parsar && mkdir -p /var/lib/parsar/.parsar && test -w /var/lib/parsar/.parsar && test -r /run/secrets/core/workspaces.json' \
    || die "server user cannot write its data directory or read Core workspace configuration"
}

stage="" locked=false starting=false committing=false downloaded=false
image_override=()
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

home="${PARSAR_HOME:-$HOME/.parsar}"
port="${PARSAR_LOCAL_PORT:-}"
bind="${PARSAR_BIND_ADDR:-}"
server_image="${PARSAR_SERVER_IMAGE:-}"
compose_source="${PARSAR_COMPOSE_FILE:-}"
dry_run=false

while [ "$#" -gt 0 ]; do
  case "$1" in
    --home|--port|--bind|--image|--compose-file)
      [ "$#" -ge 2 ] && [ -n "$2" ] && [[ "$2" != --* ]] || die "missing value for $1"
      case "$1" in
        --home) home="$2" ;;
        --port) port="$2" ;;
        --bind) bind="$2" ;;
        --image) server_image="$2" ;;
        --compose-file) compose_source="$2" ;;
      esac
      shift 2 ;;
    --dry-run) dry_run=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

if [ -n "$port" ]; then
  [[ "$port" =~ ^[0-9]{1,5}$ ]] && ((10#$port >= 1 && 10#$port <= 65535)) \
    || die "--port must be between 1 and 65535"
  port="$((10#$port))"
fi
home="$(expand_path "$home")"
[[ "$home" != *$'\n'* && "$home" != *$'\r'* && "$home" != *:* ]] || die "home path cannot contain newlines or colons"
if [ -n "$compose_source" ]; then
  compose_source="$(expand_path "$compose_source")"
  [ -f "$compose_source" ] || die "compose file does not exist: $compose_source"
fi
detect_docker
begin_install
final_env="$home/.env"
[ ! -L "$final_env" ] && { [ ! -e "$final_env" ] || [ -f "$final_env" ]; } \
  || die "$final_env must be a regular file, not a symlink"
env_file="$stage/install.env"
if [ -f "$final_env" ]; then
  cp "$final_env" "$env_file"
  cp "$final_env" "$stage/old.env"
else
  touch "$env_file"
fi

if [ -n "$compose_source" ]; then
  final_compose="$compose_source"
elif [ -f "$PWD/docker-compose.yml" ]; then
  final_compose="$PWD/docker-compose.yml"
else
  final_compose="$home/docker-compose.yml"
  [ ! -L "$final_compose" ] && { [ ! -e "$final_compose" ] || [ -f "$final_compose" ]; } \
    || die "$final_compose must be a regular file, not a symlink"
  [ ! -f "$final_compose" ] || cp "$final_compose" "$stage/old.compose"
  downloaded=true
fi
project_dir="$(cd "$(dirname "$final_compose")" && pwd -P)"
compose_file="$final_compose"
if [ "$downloaded" = true ]; then
  compose_file="$stage/docker-compose.yml"
  download_compose || die "could not download Compose configuration; existing files were not changed"
  [ -s "$compose_file" ] || die "downloaded Compose configuration is empty"
fi

if [ -n "$port" ]; then set_env PARSAR_LOCAL_PORT "$port"; else ensure_env PARSAR_LOCAL_PORT 18080; fi
if [ -n "$bind" ]; then set_env PARSAR_BIND_ADDR "$bind"; else ensure_env PARSAR_BIND_ADDR 127.0.0.1; fi
ensure_env PARSAR_PG_DATA_DIR "$home/postgres"
ensure_env PARSAR_DATA_DIR "$home/data"
ensure_env PARSAR_CORE_CONFIG_DIR "$home/core"
[ -z "$server_image" ] || set_env PARSAR_SERVER_IMAGE "$server_image"

# Exported variables take precedence over --env-file; persist managed overrides.
for key in PARSAR_PG_DATA_DIR PARSAR_DATA_DIR PARSAR_CORE_CONFIG_DIR PARSAR_PG_PASSWORD PARSAR_MASTER_KEY PARSAR_IMAGE_PULL_POLICY; do
  if [ -n "${!key:-}" ]; then
    value="${!key}"
    case "$key" in *_DIR) value="$(expand_path "$value")" ;; esac
    set_env "$key" "$value"
  fi
done
unset PARSAR_LOCAL_PORT PARSAR_BIND_ADDR PARSAR_SERVER_IMAGE PARSAR_PG_DATA_DIR
unset PARSAR_DATA_DIR PARSAR_CORE_CONFIG_DIR PARSAR_PG_PASSWORD PARSAR_MASTER_KEY PARSAR_IMAGE_PULL_POLICY

generated_secrets=false
existing_secrets=false
if [ -f "$final_env" ] && { has_env PARSAR_PG_PASSWORD || has_env PARSAR_MASTER_KEY; }; then existing_secrets=true; fi
for key in PARSAR_PG_PASSWORD PARSAR_MASTER_KEY; do
  if ! has_env "$key"; then
    if [ "$existing_secrets" = true ]; then
      die "$key is missing; restore its original value in $final_env, or supply both secrets when preconfiguring a new installation"
    fi
    if [ "$key" = PARSAR_PG_PASSWORD ]; then size=24; else size=32; fi
    set_env "$key" "$(random_hex "$size")"
    generated_secrets=true
  fi
done

log "Using $final_compose"
log "Using $final_env"
cat >"$stage/validate.yml" <<'YAML'
services:
  postgres:
    environment:
      POSTGRES_PASSWORD: ${PARSAR_PG_PASSWORD:?Set a nonempty database password}
  parsar-server:
    environment:
      PARSAR_MASTER_KEY: ${PARSAR_MASTER_KEY:?Set a nonempty master key}
YAML
compose -f "$stage/validate.yml" config --quiet || die "invalid Compose configuration; existing files were not changed"
existing_containers="$(compose ps -aq)" || die "could not inspect the existing Compose project"
if [ "$generated_secrets" = true ] && [ -n "$existing_containers" ]; then
  die "existing containers have no matching secrets; restore $final_env before upgrading"
fi
services="$(compose config --services)"
for service in postgres parsar-server; do
  printf '%s\n' "$services" | grep -qx "$service" || die "Compose configuration is missing $service"
done
if [ "$dry_run" = true ]; then
  log "Dry run complete; configuration and containers were not changed"
  exit 0
fi

cat >"$stage/prepare.yml" <<'YAML'
services:
  parsar-server:
    volumes:
      - type: bind
        source: ${PARSAR_CORE_CONFIG_DIR:?Core configuration directory is required}
        target: /run/secrets/core
        read_only: false
YAML
log "Pulling images"
compose pull || die "image pull failed; existing configuration and containers were not changed"
cat >"$stage/local-images.yml" <<'YAML'
services:
  postgres:
    pull_policy: never
  parsar-server:
    pull_policy: never
YAML
image_override=(-f "$stage/local-images.yml")
if [ "$generated_secrets" = true ]; then verify_empty_data; fi

committing=true
if [ "$downloaded" = true ]; then mv "$compose_file" "$final_compose"; fi
mv "$env_file" "$final_env"
compose_file="$final_compose"
env_file="$final_env"
# Once preparation can write mounted data, keep the matching configuration on failure.
starting=true
log "Preparing server data and Core configuration"
prepare_data_directory
image_override=()
log "Starting Parsar"
if ! compose up -d --pull never || ! wait_for_health; then
  compose ps >&2 || true
  compose logs --tail 80 postgres parsar-server >&2 || true
  die "services did not become ready; configuration and data were retained. Inspect the logs and rerun the same command."
fi

address="$(compose port parsar-server 8080)"
log "Parsar is running at http://$address"
log "Create the first owner account in the web setup flow."
printf '\nManage the stack:\n'
for action in ps 'logs -f' down; do
  printf '  '
  printf '%q ' "${DOCKER[@]}" compose --project-directory "$project_dir" -f "$compose_file" --env-file "$env_file"
  printf '%s\n' "$action"
done
