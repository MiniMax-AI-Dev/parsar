#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# shellcheck source=scripts/dev-env.sh
source "$ROOT_DIR/scripts/dev-env.sh"

export DATABASE_URL="${DATABASE_URL:-$(parsar_dev_database_url)}"
export PARSAR_MIGRATIONS_DIR="${PARSAR_MIGRATIONS_DIR:-$ROOT_DIR/server/migrations}"
export AGENT_DAEMON_SANDBOX_BACKEND="${AGENT_DAEMON_SANDBOX_BACKEND:-docker}"
export AGENT_DAEMON_SANDBOX_DOCKER_IMAGE="${AGENT_DAEMON_SANDBOX_DOCKER_IMAGE:-${PARSAR_SANDBOX_IMAGE:-ghcr.io/minimax-ai-dev/parsar-sandbox:latest}}"

exec "$@"
