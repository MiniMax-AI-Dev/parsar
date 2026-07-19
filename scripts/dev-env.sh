#!/usr/bin/env bash
#
# Single source of truth for Parsar LOCAL DEV defaults.
#
# `source` this file from any dev script BEFORE it needs the dev
# Postgres credentials / database name / default ports / dev-auth
# posture. Every value is override-friendly: pre-set the env var and
# it wins (the `${VAR:-default}` form below only fills in the blank).
#
# Consumers:
#   scripts/dev-stack.sh      (make dev-db; make dev is a compatibility alias)
#   scripts/dev-all.sh        (make dev-all)
#   scripts/dev-server-up.sh  (make dev-server-up)
#   docker-compose.dev.yml    (reads the exported PARSAR_PG_* values;
#                              its inline `${VAR:-default}` fallbacks are
#                              kept in sync with the defaults here so a
#                              bare `docker compose up` still works)
#
# DEV-ONLY scope: these are throwaway local values (dev_auth shim on,
# well-known DB creds). They deliberately do NOT live in
# docs/deploy/config*.yaml, which model the prod-posture profiles.

# ── Postgres identity (dev container creds + database name) ──────────
export PARSAR_PG_USER="${PARSAR_PG_USER:-parsar}"
export PARSAR_PG_PASSWORD="${PARSAR_PG_PASSWORD:-parsar}"
export PARSAR_PG_DB="${PARSAR_PG_DB:-parsar_dev}"

# ── Host-side ports ──────────────────────────────────────────────────
# Host the dev Postgres container publishes its 5432 on.
export PARSAR_POSTGRES_HOST="${PARSAR_POSTGRES_HOST:-127.0.0.1}"
export PARSAR_POSTGRES_PORT="${PARSAR_POSTGRES_PORT:-15432}"
# Default listen port for the persistent dev server (dev-server-up.sh).
export PARSAR_DEV_SERVER_PORT="${PARSAR_DEV_SERVER_PORT:-18080}"
# Vite web dev server port.
export PARSAR_WEB_PORT="${PARSAR_WEB_PORT:-5173}"

# ── Auth posture ─────────────────────────────────────────────────────
# Local dev runs with the X-Parsar-Dev-User-ID shim enabled. This
# MUST stay false in any deployed profile (Validate() rejects dev_auth
# in prod) — see docs/deploy/config.example.yaml.
export PARSAR_DEV_AUTH="${PARSAR_DEV_AUTH:-true}"

# ── Server runtime posture ───────────────────────────────────────────
# Loopback public_url selects the development validation profile. These
# values are intentionally safe only for local development and remain
# override-friendly for alternate ports or integration setups.
export PARSAR_ADDR="${PARSAR_ADDR:-127.0.0.1:$PARSAR_DEV_SERVER_PORT}"
export PARSAR_PUBLIC_URL="${PARSAR_PUBLIC_URL:-http://127.0.0.1:$PARSAR_DEV_SERVER_PORT}"
export PARSAR_MASTER_KEY="${PARSAR_MASTER_KEY:-parsar-dev-master-key-2026}"
export PARSAR_FEISHU_MOCK="${PARSAR_FEISHU_MOCK:-true}"
export PARSAR_AGENT_DAEMON_OWNER_URL="${PARSAR_AGENT_DAEMON_OWNER_URL:-$PARSAR_PUBLIC_URL}"

# Builds the dev DATABASE_URL from the parts above. Optional args let a
# caller override host ($1) and port ($2) without touching the creds —
# used by dev-all.sh, which picks an ephemeral free port for Postgres.
parsar_dev_database_url() {
  local host="${1:-$PARSAR_POSTGRES_HOST}"
  local port="${2:-$PARSAR_POSTGRES_PORT}"
  printf 'postgres://%s:%s@%s:%s/%s?sslmode=disable' \
    "$PARSAR_PG_USER" "$PARSAR_PG_PASSWORD" \
    "$host" "$port" "$PARSAR_PG_DB"
}

# Start (or attach to) the development Postgres container defined by
# docker-compose.dev.yml and wait for it to accept SQL connections.
# Used by `make dev-db` and the `check-store` CI job. Keeping the
# orchestration in this file means `scripts/dev-stack.sh` can stay a
# thin shim and other Makefile targets can call the same code path.
parsar_dev_start_db() {
  ROOT_DIR="${ROOT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
  PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"
  PARSAR_LOG_DIR="$PARSAR_HOME/logs"
  PARSAR_STATE_DIR="$PARSAR_HOME/state"
  PARSAR_DEV_DIR="$PARSAR_HOME/dev"

  "$ROOT_DIR/scripts/setup.sh" >/dev/null
  mkdir -p "$PARSAR_LOG_DIR" "$PARSAR_STATE_DIR" "$PARSAR_DEV_DIR"

  "$ROOT_DIR/scripts/dev-compose.sh" up -d postgres

  printf 'Waiting for Postgres'
  for _ in {1..30}; do
    if "$ROOT_DIR/scripts/dev-compose.sh" exec -T postgres \
      pg_isready -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" >/dev/null 2>&1; then
      printf ' ready\n'
      break
    fi
    printf '.'
    sleep 1
  done

  if ! "$ROOT_DIR/scripts/dev-compose.sh" exec -T postgres \
    pg_isready -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" >/dev/null 2>&1; then
    printf '\nPostgres did not become ready; inspect logs with:\n' >&2
    printf '  ./scripts/dev-compose.sh logs postgres\n' >&2
    return 1
  fi

  cat <<INFO
Parsar dev stack started.

Postgres: ${PARSAR_POSTGRES_HOST}:${PARSAR_POSTGRES_PORT} (db=${PARSAR_PG_DB} user=${PARSAR_PG_USER} password=${PARSAR_PG_PASSWORD})
Migrate:  make migrate-dev
Server:   make server             # http://127.0.0.1:${PARSAR_DEV_SERVER_PORT}/api/v1/health
Web:      make web                # http://127.0.0.1:${PARSAR_WEB_PORT}
Runner:   make http-runner-loop   # bounded local HTTP Agent runner
Logs:     $PARSAR_LOG_DIR
INFO
}
