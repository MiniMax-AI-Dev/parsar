#!/usr/bin/env bash
set -euo pipefail

./scripts/setup.sh >/dev/null

(
  cd server
  before_sqlc_status="$(git status --short -- internal/db/sqlc)"
  # sqlc pinned to v1.29.0 — v1.31.1 declares `go >= 1.26` in its
  # go.mod, which forces `go run` to fetch a newer toolchain than
  # this repo builds under (go 1.25.7). Bump both this script AND
  # the Makefile target when moving off v1.29.0.
  go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate
  after_sqlc_status="$(git status --short -- internal/db/sqlc)"
  if [[ "$before_sqlc_status" != "$after_sqlc_status" ]]; then
    echo "sqlc generated files are out of date" >&2
    printf '%s\n' "$after_sqlc_status" >&2
    exit 1
  fi
)

go test -p 1 ./server/...

./scripts/check-migrations.sh

if [[ ! -d node_modules ]]; then
  pnpm install
fi

pnpm typecheck

for polluted in .parsar logs state cache config; do
  if [[ -e "$polluted" ]]; then
    echo "CWD pollution detected: $polluted" >&2
    exit 1
  fi
done

if grep -R "[>/]tmp/parsar" scripts server --exclude='check.sh' >/dev/null 2>&1; then
  echo "Runtime logs/state must live under ~/.parsar, not /tmp/parsar*" >&2
  exit 1
fi

printf 'Parsar harness checks passed.\n'
