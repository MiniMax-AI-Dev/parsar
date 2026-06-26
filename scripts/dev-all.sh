#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"
PARSAR_LOG_DIR="$PARSAR_HOME/logs"
PARSAR_STATE_DIR="$PARSAR_HOME/state"
mkdir -p "$PARSAR_LOG_DIR" "$PARSAR_STATE_DIR"

free_port() {
  python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

# Pick an ephemeral Postgres port BEFORE sourcing dev-env.sh so its
# default (15432) only fills the blank — dev-all intentionally runs on a
# free port to avoid colliding with a `make dev` stack.
if [[ -z "${PARSAR_POSTGRES_PORT:-}" ]]; then
  PARSAR_POSTGRES_PORT="$(free_port)"
  export PARSAR_POSTGRES_PORT
fi

# shellcheck source=scripts/dev-env.sh
source "$ROOT_DIR/scripts/dev-env.sh"

API_ADDR="${PARSAR_ADDR:-127.0.0.1:8080}"
WEB_PORT="$PARSAR_WEB_PORT"
DATABASE_URL="${DATABASE_URL:-$(parsar_dev_database_url)}"
export DATABASE_URL

./scripts/setup.sh >/dev/null
docker compose -f docker-compose.dev.yml up -d postgres >/dev/null

for _ in {1..30}; do
  if docker compose -f docker-compose.dev.yml exec -T postgres pg_isready -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker compose -f docker-compose.dev.yml exec -T postgres pg_isready -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" >/dev/null 2>&1; then
  echo "Postgres did not become ready" >&2
  exit 1
fi

(
  cd server
  go run ./cmd/migrate
  go run ./cmd/seeddev
)

server_log="$PARSAR_LOG_DIR/server.log"
web_log="$PARSAR_LOG_DIR/web.log"
runner_log="$PARSAR_LOG_DIR/http-runner.log"
runner_status="$PARSAR_STATE_DIR/http-runner-status.json"
pid_file="$PARSAR_STATE_DIR/dev-all.pids"

cleanup_existing() {
  if [[ -f "$pid_file" ]]; then
    while read -r pid; do
      if [[ -n "$pid" ]]; then kill "$pid" >/dev/null 2>&1 || true; fi
    done < "$pid_file"
  fi
  : > "$pid_file"
}

cleanup_existing

(
  cd server
  PARSAR_ADDR="$API_ADDR" DATABASE_URL="$DATABASE_URL" go run ./cmd/server
) >"$server_log" 2>&1 &
SERVER_PID=$!
printf '%s\n' "$SERVER_PID" >> "$pid_file"

for _ in {1..30}; do
  if curl -fsS "http://$API_ADDR/api/v1/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -fsS "http://$API_ADDR/api/v1/health" >/dev/null 2>&1; then
  echo "Parsar API did not become ready; see $server_log" >&2
  exit 1
fi

PARSAR_DEV_API_URL="http://$API_ADDR" pnpm --filter @parsar/web exec vite --host 127.0.0.1 --port "$WEB_PORT" >"$web_log" 2>&1 &
WEB_PID=$!
printf '%s\n' "$WEB_PID" >> "$pid_file"

(
  cd server
  PARSAR_HTTP_RUNNER_LOG="$runner_log" PARSAR_HTTP_RUNNER_STATUS="$runner_status" DATABASE_URL="$DATABASE_URL" go run ./cmd/httprunner --interval "${PARSAR_HTTP_RUNNER_INTERVAL:-2s}" --max-runs "${PARSAR_HTTP_RUNNER_MAX_RUNS:-100}"
) >"$runner_log" 2>&1 &
RUNNER_PID=$!
printf '%s\n' "$RUNNER_PID" >> "$pid_file"

cat <<INFO
Parsar local dev workflow started.

API:        http://$API_ADDR/api/v1/health
Web:        http://127.0.0.1:$WEB_PORT/
Runner:     pid $RUNNER_PID, bounded by PARSAR_HTTP_RUNNER_MAX_RUNS=${PARSAR_HTTP_RUNNER_MAX_RUNS:-100}
PID file:   $pid_file
Status:     $runner_status
Logs:
  API:      $server_log
  Web:      $web_log
  Runner:   $runner_log

Stop with:
  while read -r process_id; do kill "\$process_id" 2>/dev/null || true; done < "$pid_file"
INFO
