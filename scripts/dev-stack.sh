#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/dev-env.sh
source "$SCRIPT_DIR/dev-env.sh"

./scripts/setup.sh

PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"
PARSAR_LOG_DIR="$PARSAR_HOME/logs"
PARSAR_STATE_DIR="$PARSAR_HOME/state"
PARSAR_DEV_DIR="$PARSAR_HOME/dev"
mkdir -p "$PARSAR_LOG_DIR" "$PARSAR_STATE_DIR" "$PARSAR_DEV_DIR"

docker compose -f docker-compose.dev.yml up -d postgres
cat <<INFO
Parsar dev stack started.

Postgres: ${PARSAR_POSTGRES_HOST}:${PARSAR_POSTGRES_PORT} (db=${PARSAR_PG_DB} user=${PARSAR_PG_USER} password=${PARSAR_PG_PASSWORD})
Migrate:  make migrate-dev
Seed:     make seed-dev-db
Server:   make server             # http://127.0.0.1:8080/api/v1/health
Web:      make web                # http://127.0.0.1:${PARSAR_WEB_PORT}
Runner:   make http-runner-loop   # bounded local HTTP Agent runner
Logs:     $PARSAR_LOG_DIR
INFO
