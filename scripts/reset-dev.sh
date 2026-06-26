#!/usr/bin/env bash
set -euo pipefail

PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"

if [[ "${1:-}" == "--all" ]]; then
  docker compose -f docker-compose.dev.yml down -v --remove-orphans
else
  docker compose -f docker-compose.dev.yml down --remove-orphans
fi

mkdir -p "$PARSAR_HOME/dev"
printf 'Parsar dev stack reset. Runtime root remains %s\n' "$PARSAR_HOME"
