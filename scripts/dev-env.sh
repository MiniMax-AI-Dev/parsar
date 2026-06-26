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
#   scripts/dev-stack.sh      (make dev)
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
