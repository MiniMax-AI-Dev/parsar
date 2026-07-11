#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose=(docker compose --project-name parsar --file "$ROOT_DIR/docker-compose.dev.yml")

if docker info >/dev/null 2>&1; then
  exec "${compose[@]}" "$@"
fi

if command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
  exec sudo -n "${compose[@]}" "$@"
fi

echo "Docker is unavailable. Start Docker or grant this user access to its socket." >&2
exit 1
