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

PARSAR_CHECK_DATABASE_URL="${DATABASE_URL:-$(parsar_dev_database_url)}"
export PARSAR_CHECK_DATABASE_URL
export PARSAR_TEST_DATABASE_URL="$PARSAR_CHECK_DATABASE_URL"

# Wait for the same host-published endpoint that Go tests will use.
# The Postgres image briefly starts an internal bootstrap server during
# initdb, so a TCP SQL round-trip is the only readiness signal that
# proves the final server and mapped port are usable.
pg_connect_ready=0
for _ in $(seq 1 60); do
  if docker compose -f docker-compose.dev.yml exec -T postgres \
    env PGPASSWORD="$PARSAR_PG_PASSWORD" \
    psql -h 127.0.0.1 -p 5432 -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" -c 'select 1' >/dev/null 2>&1; then
    pg_connect_ready=1
    break
  fi
  sleep 1
done

if [[ "$pg_connect_ready" -ne 1 ]]; then
  echo "Postgres did not accept SQL connections within 60s" >&2
  docker compose -f docker-compose.dev.yml logs --tail=80 postgres >&2 || true
  exit 1
fi

(
  cd server
  DATABASE_URL="$PARSAR_CHECK_DATABASE_URL" PARSAR_MIGRATIONS_DIR="$PWD/migrations" go run ./cmd/migrate
)

go test ./server/internal/store -count=1

echo "Parsar migration and store integration smoke test passed."
