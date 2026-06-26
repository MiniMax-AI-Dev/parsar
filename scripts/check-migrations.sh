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

PARSAR_CHECK_DATABASE_URL="${DATABASE_URL:-$(parsar_dev_database_url)}"
export PARSAR_CHECK_DATABASE_URL
export PARSAR_TEST_DATABASE_URL="$PARSAR_CHECK_DATABASE_URL"

(
  cd server
  DATABASE_URL="$PARSAR_CHECK_DATABASE_URL" PARSAR_MIGRATIONS_DIR="$PWD/migrations" go run ./cmd/migrate
  DATABASE_URL="$PARSAR_CHECK_DATABASE_URL" go run ./cmd/seeddev
)

# -p 1 serializes packages: store tests and seed tests both use the same
# Postgres instance and both TRUNCATE the same tables; running them in
# parallel can deadlock on `pg_class` cascade locks.
go test ./server/internal/store ./server/internal/seed -count=1 -p 1

echo "Parsar migration and store integration smoke test passed."
