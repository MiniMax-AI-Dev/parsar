#!/usr/bin/env bash
#
# Start the Parsar dev server with a stable footprint.
#
# Why this script exists
# ----------------------
# Operating-team pain points seen on macOS sandboxes:
#  - launchctl plist files written to /tmp get cleaned silently and
#    bootout fails with "Operation not permitted" / "Input/output error".
#  - The compiled `parsar-server` binary parked under /tmp also gets
#    cleaned, so the next `launchctl load` fails with "No such file".
#  - The Postgres docker container exposes 5432 on a random host port
#    that changes every `docker restart` — hardcoded DATABASE_URLs in
#    plists go stale silently.
#  - `nohup ... &` inside a sandbox bash session is still attached to
#    that session's process group; when the agent's sandbox exits, the
#    server gets SIGTERM'd.
#
# This script gives the server a persistent home under ~/.parsar/bin/,
# detects the live Postgres port automatically, and runs it inside a
# tmux session that survives sandbox bash exits.
#
# Usage: scripts/dev-server-up.sh [--rebuild] [--port 18080]
set -euo pipefail

# Single source of dev defaults (PG creds/db, default ports, dev_auth).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/dev-env.sh
source "$SCRIPT_DIR/dev-env.sh"

BIN_DIR="${HOME}/.parsar/bin"
LOG_DIR="${HOME}/.parsar/logs"
BIN_PATH="${BIN_DIR}/parsar-server"
LOG_PATH="${LOG_DIR}/server.log"
TMUX_SESSION="parsar-server"
# Preferred container name (back-compat). When it isn't running we fall
# back to resolving the compose service, which is auto-named
# "<project>-postgres-1" — and the project defaults to the checkout dir,
# so a worktree named e.g. "im-history-tool" produces
# "im-history-tool-postgres-1", not "parsar-postgres-1".
PG_CONTAINER="${PARSAR_PG_CONTAINER:-parsar-postgres-1}"
COMPOSE_FILE_REL="docker-compose.dev.yml"

REBUILD=0
PORT="${PARSAR_ADDR_PORT:-${PARSAR_DEV_SERVER_PORT}}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --rebuild) REBUILD=1; shift ;;
    --port)    PORT="$2"; shift 2 ;;
    -h|--help)
      cat <<HELP
Usage: $0 [--rebuild] [--port <port>]

  --rebuild   Rebuild the server binary from server/cmd/server before starting
  --port      Override the listen port (default ${PARSAR_DEV_SERVER_PORT})

The server runs inside tmux session '${TMUX_SESSION}'. Tail logs with:
  scripts/dev-server-log.sh
Stop with:
  scripts/dev-server-down.sh
HELP
      exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

mkdir -p "${BIN_DIR}" "${LOG_DIR}"

# ── locate repo root (scripts/ sits one level under it) ──────────────
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ── rebuild if needed (always rebuild when binary is missing) ────────
if [[ ! -x "${BIN_PATH}" || "${REBUILD}" -eq 1 ]]; then
  echo "[dev-server-up] building ${BIN_PATH} from ${REPO_ROOT}/server/cmd/server"
  ( cd "${REPO_ROOT}/server" && go build -o "${BIN_PATH}" ./cmd/server )
fi

# ── preflight required CLIs ──────────────────────────────────────────
# Bail with a helpful error rather than failing later inside docker /
# tmux / curl calls. macOS ships curl, but explicit is cheaper than
# trace-reading.
for cli in docker tmux curl; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "[dev-server-up] ${cli} not found in PATH" >&2
    case "${cli}" in
      docker) echo "                install Docker Desktop or 'brew install --cask docker'" >&2 ;;
      tmux)   echo "                install via 'brew install tmux'" >&2 ;;
      curl)   echo "                install via 'brew install curl' (or use macOS bundled)" >&2 ;;
    esac
    exit 1
  fi
done

# ── resolve the Postgres container's published 5432 port ─────────────
# The compose file publishes 5432 on a host port that can drift, so we
# always ask docker for the live mapping rather than trusting a constant.
COMPOSE_FILE="${REPO_ROOT}/${COMPOSE_FILE_REL}"

resolve_pg_port() {
  # 1) explicit / back-compat container name
  local p
  p="$(docker port "${PG_CONTAINER}" 5432 2>/dev/null | sed -E 's/.*:([0-9]+)$/\1/' | head -1 || true)"
  if [[ -n "${p}" ]]; then echo "${p}"; return 0; fi
  # 2) compose service (auto-named "<project>-postgres-1"); project name
  #    defaults to the checkout dir, so this Just Works in a worktree.
  local cid
  cid="$(docker compose -f "${COMPOSE_FILE}" ps -q postgres 2>/dev/null | head -1 || true)"
  if [[ -n "${cid}" ]]; then
    docker port "${cid}" 5432 2>/dev/null | sed -E 's/.*:([0-9]+)$/\1/' | head -1
  fi
}

PG_PORT="$(resolve_pg_port)"
if [[ -z "${PG_PORT}" ]]; then
  echo "[dev-server-up] no Postgres container running; starting compose service 'postgres'"
  docker compose -f "${COMPOSE_FILE}" up -d postgres
  # wait for the container to report healthy before probing its port
  cid="$(docker compose -f "${COMPOSE_FILE}" ps -q postgres 2>/dev/null | head -1 || true)"
  if [[ -n "${cid}" ]]; then
    echo -n "[dev-server-up] waiting for postgres health "
    for _ in $(seq 1 30); do
      if [[ "$(docker inspect -f '{{.State.Health.Status}}' "${cid}" 2>/dev/null)" == "healthy" ]]; then
        echo " ok"; break
      fi
      sleep 1; echo -n "."
    done
  fi
  PG_PORT="$(resolve_pg_port)"
fi
if [[ -z "${PG_PORT}" ]]; then
  echo "[dev-server-up] could not resolve Postgres port (tried container '${PG_CONTAINER}' and compose service 'postgres')" >&2
  echo "                hint: start the dev DB with 'docker compose -f ${COMPOSE_FILE_REL} up -d postgres'" >&2
  exit 1
fi
DATABASE_URL="$(parsar_dev_database_url 127.0.0.1 "${PG_PORT}")"
echo "[dev-server-up] postgres port: ${PG_PORT}"

# ── apply migrations (idempotent; goose skips already-applied) ───────
# A fresh dev DB has no schema, so the server would boot then fail every
# query with "relation ... does not exist". Migrate before starting.
echo "[dev-server-up] applying migrations"
( cd "${REPO_ROOT}/server" \
  && DATABASE_URL="${DATABASE_URL}" \
     PARSAR_MIGRATIONS_DIR="${REPO_ROOT}/server/migrations" \
     go run ./cmd/migrate )

# ── stop any prior incarnation ───────────────────────────────────────
if tmux has-session -t "${TMUX_SESSION}" 2>/dev/null; then
  echo "[dev-server-up] killing existing tmux session '${TMUX_SESSION}'"
  tmux kill-session -t "${TMUX_SESSION}" || true
fi
# Best-effort kill any stray process holding the binary path
pkill -f "${BIN_PATH}" 2>/dev/null || true
sleep 1

# ── start under tmux (survives sandbox bash exit) ────────────────────
# The server refuses to boot without either real Feishu creds or mock
# mode, and without a resolvable agent_daemon owner URL. Supply dev
# defaults here (override by pre-exporting the same vars).
FEISHU_MOCK="${PARSAR_FEISHU_MOCK:-true}"
OWNER_URL="${PARSAR_AGENT_DAEMON_OWNER_URL:-http://127.0.0.1:${PORT}}"
tmux new-session -d -s "${TMUX_SESSION}" \
  "PARSAR_ADDR=:${PORT} DATABASE_URL='${DATABASE_URL}' PARSAR_DEV_AUTH=${PARSAR_DEV_AUTH} PARSAR_FEISHU_MOCK=${FEISHU_MOCK} PARSAR_AGENT_DAEMON_OWNER_URL='${OWNER_URL}' '${BIN_PATH}' 2>&1 | tee '${LOG_PATH}'"

# ── readiness probe ──────────────────────────────────────────────────
echo -n "[dev-server-up] waiting for :${PORT} "
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  sleep 1
  if curl -sf -o /dev/null -m 2 "http://127.0.0.1:${PORT}/api/v1/me/workspaces" \
       -H "X-Parsar-Dev-User-ID: 00000000-0000-0000-0000-000000000001"; then
    echo " ready"
    echo "[dev-server-up] server live at http://127.0.0.1:${PORT}"
    echo "                logs: tail -F ${LOG_PATH}"
    echo "                tmux: tmux attach -t ${TMUX_SESSION}"
    exit 0
  fi
  echo -n "."
done

echo " timeout" >&2
echo "[dev-server-up] last 30 log lines:" >&2
tail -30 "${LOG_PATH}" >&2 || true
exit 1
