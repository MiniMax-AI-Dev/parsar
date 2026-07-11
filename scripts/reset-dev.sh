#!/usr/bin/env bash
set -euo pipefail

PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"

if [[ "${1:-}" == "--all" ]]; then
  ./scripts/dev-compose.sh down -v --remove-orphans
else
  ./scripts/dev-compose.sh down --remove-orphans
fi

mkdir -p "$PARSAR_HOME/dev"
printf 'Parsar dev stack reset. Runtime root remains %s\n' "$PARSAR_HOME"
