#!/usr/bin/env bash
set -euo pipefail

./scripts/setup.sh >/dev/null

(
  cd server
  before_sqlc_status="$(git status --short -- internal/db/sqlc)"
  # sqlc pinned to v1.29.0 — v1.31.1 declares `go >= 1.26` in its
  # go.mod, which forces `go run` to fetch a newer toolchain than
  # this repo builds under (go 1.25.12). Bump both this script AND
  # the Makefile target when moving off v1.29.0.
  go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate
  after_sqlc_status="$(git status --short -- internal/db/sqlc)"
  if [[ "$before_sqlc_status" != "$after_sqlc_status" ]]; then
    echo "sqlc generated files are out of date" >&2
    printf '%s\n' "$after_sqlc_status" >&2
    exit 1
  fi
)

go test $(cd server && go list ./... | grep -Ev 'internal/(store|seed)$')

# store + seed share the same Postgres test database; keep them serial
# with -p 1. Cross-package serialisation inside store is already done
# via pg_advisory_lock(8675309) in openTestDB, so limiting the outer
# `go test` runner is only about not letting seed and store trample
# each other's TRUNCATE.
go test -p 1 ./server/internal/store ./server/internal/seed

./scripts/check-migrations.sh

if [[ ! -d node_modules ]]; then
  pnpm install
fi

pnpm typecheck

if (cd apps/web && npx eslint src/ 2>&1) | grep -q "no-restricted-syntax"; then
  echo "Design-system lint violations (arbitrary font sizes or raw palette):" >&2
  (cd apps/web && npx eslint src/ 2>&1) | grep "no-restricted-syntax" >&2
  exit 1
fi

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
