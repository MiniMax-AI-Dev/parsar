#!/usr/bin/env bash
set -euo pipefail

if ! docker info >/dev/null 2>&1; then
  echo "Docker is not available; skipping Postgres migration smoke test." >&2
  exit 0
fi

./scripts/setup.sh >/dev/null

if [[ -z "${PARSAR_POSTGRES_PORT:-}" ]]; then
  PARSAR_POSTGRES_PORT="$(python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
  export PARSAR_POSTGRES_PORT
fi

# Pull dev PG creds / db name from the single source (after the port is
# chosen so its default doesn't override the free port picked above).
# shellcheck source=scripts/dev-env.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/dev-env.sh"

docker compose -f docker-compose.dev.yml down -v --remove-orphans >/dev/null 2>&1 || true
docker compose -f docker-compose.dev.yml up -d postgres >/dev/null

# Wait for Postgres to actually accept connections. pg_isready can return
# 0 (accepting) only after the entrypoint finishes initdb and the
# postmaster binds 5432 — on cold cache CI that takes 5–15s. We poll
# up to 60s and treat any non-zero exit as "not yet". The previous
# loop double-checked outside the loop, which raced the initdb window
# (#12 follow-up: CI hit "Postgres did not become ready" 1.5s after
# Started because the very first probe transient-failed and a second
# probe was issued before postmaster was up).
pg_ready=0
for _ in $(seq 1 60); do
  if docker compose -f docker-compose.dev.yml exec -T postgres pg_isready -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" >/dev/null 2>&1; then
    pg_ready=1
    break
  fi
  sleep 1
done

if [[ "$pg_ready" -ne 1 ]]; then
  echo "Postgres did not become ready within 60s" >&2
  docker compose -f docker-compose.dev.yml logs --tail=80 postgres >&2 || true
  exit 1
fi

PARSAR_CHECK_DATABASE_URL="${DATABASE_URL:-$(parsar_dev_database_url)}"
export PARSAR_CHECK_DATABASE_URL
export PARSAR_TEST_DATABASE_URL="$PARSAR_CHECK_DATABASE_URL"

(
  cd server
  DATABASE_URL="$PARSAR_CHECK_DATABASE_URL" PARSAR_MIGRATIONS_DIR="$PWD/migrations" go run ./cmd/migrate
)

# -p 1 serializes packages: store tests and seed tests both use the same
# Postgres instance and both TRUNCATE the same tables; running them in
# parallel can deadlock on `pg_class` cascade locks.
go test ./server/internal/store ./server/internal/seed -count=1 -p 1

echo "Parsar migration and store integration smoke test passed."
