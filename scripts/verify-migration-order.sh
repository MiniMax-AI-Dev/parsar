#!/usr/bin/env bash
# Enforce two invariants on server/migrations/ against a base ref:
#   1. No file that exists on the base ref may be modified — once a
#      migration lands on main, prod has run it and any edit only
#      affects fresh installs, splitting the schema.
#   2. Every newly-added migration's numeric prefix (NNNNNN_*) must be
#      strictly greater than the maximum prefix present on the base
#      ref. Gaps are fine (5 -> 10); regressions are not, because
#      goose orders files by that prefix and would silently skip a
#      migration inserted below the current head.
#
# Usage: verify-migration-order.sh [base-ref]
# Default base ref: origin/main.
set -euo pipefail

# Pre-launch escape hatch: set PARSAR_ALLOW_MIGRATION_EDIT=1 to skip
# the immutability check when prod has not run these migrations yet.
if [[ "${PARSAR_ALLOW_MIGRATION_EDIT:-}" == "1" ]]; then
  echo "PARSAR_ALLOW_MIGRATION_EDIT=1 — skipping migration immutability check."
  exit 0
fi

BASE_REF="${1:-origin/main}"
MIG_DIR="server/migrations"

if ! git rev-parse --verify --quiet "${BASE_REF}" >/dev/null; then
  echo "verify-migration-order: base ref '${BASE_REF}' not found; run 'git fetch origin main' first." >&2
  exit 2
fi

# Name-status diff scoped to migration SQL. -M0 disables rename detection
# so a rename shows up as delete+add (which we treat as a modification
# of the deleted file, i.e. failure — you can't retitle a landed
# migration either).
added=()
modified=()
while IFS=$'\t' read -r status path extra; do
  [[ -z "${status}" ]] && continue
  # Rename/copy lines have status=R100 and two tab-separated paths;
  # the new path is in $extra. We only care about the old path here
  # because it counts as a removal of a landed file.
  case "${status}" in
    A)   added+=("${path}") ;;
    M|T) modified+=("${path}") ;;
    D)   modified+=("${path}") ;;
    R*|C*)
      modified+=("${path}")
      if [[ -n "${extra}" ]]; then
        added+=("${extra}")
      fi
      ;;
    *)   modified+=("${path}") ;;
  esac
done < <(git diff --name-status -M0 "${BASE_REF}"...HEAD -- "${MIG_DIR}/*.sql")

fail=0

if ((${#modified[@]} > 0)); then
  fail=1
  echo "::error::Migrations that already exist on ${BASE_REF} were modified or removed:"
  for f in "${modified[@]}"; do
    echo "  - ${f}"
  done
  echo "Migrations become immutable the moment they land on main — prod has"
  echo "already applied them. To change schema, add a NEW migration with the"
  echo "next number instead of editing history."
fi

extract_num() {
  local base="${1##*/}"
  local num="${base%%_*}"
  if [[ ! "${num}" =~ ^[0-9]+$ ]]; then
    echo "verify-migration-order: cannot parse numeric prefix from '${1}'" >&2
    return 1
  fi
  # Strip leading zeros without invoking octal parsing.
  printf '%d\n' "$((10#${num}))"
}

# Highest prefix on the base ref. If there are no migrations there yet,
# treat the floor as 0 so the first migration ever can be numbered 1.
base_max=0
while IFS= read -r f; do
  [[ -z "${f}" ]] && continue
  n=$(extract_num "${f}") || exit 3
  ((n > base_max)) && base_max=${n}
done < <(git ls-tree -r --name-only "${BASE_REF}" -- "${MIG_DIR}" | grep -E '\.sql$' || true)

if ((${#added[@]} > 0)); then
  # New migrations must land strictly above base_max AND must not
  # collide with each other. Sort them numerically and walk upwards.
  entries=""
  for f in "${added[@]}"; do
    n=$(extract_num "${f}") || exit 3
    entries+="${n}:${f}"$'\n'
  done
  sorted=()
  while IFS= read -r line; do
    [[ -z "${line}" ]] && continue
    sorted+=("${line}")
  done < <(printf '%s' "${entries}" | sort -t: -k1,1n)

  prev=${base_max}
  for entry in "${sorted[@]}"; do
    n="${entry%%:*}"
    f="${entry#*:}"
    if ((n <= prev)); then
      fail=1
      if ((n <= base_max)); then
        echo "::error::New migration ${f} (prefix ${n}) is not above the current head on ${BASE_REF} (max ${base_max})."
        echo "  goose applies files in lexicographic order — a lower-numbered file"
        echo "  inserted below the head will be skipped on any DB that has already"
        echo "  migrated past it. Renumber to $((base_max + 1)) or later."
      else
        echo "::error::New migration ${f} (prefix ${n}) collides with another added migration."
      fi
    fi
    prev=${n}
  done
fi

if ((fail != 0)); then
  exit 1
fi

echo "Migration order check passed."
if ((${#added[@]} > 0)); then
  echo "  Base head: ${base_max}. Added: ${added[*]}."
else
  echo "  No migration changes in this diff."
fi
