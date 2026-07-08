#!/usr/bin/env bash
# One-command Parsar server with embedded PostgreSQL + frontend.
# No Docker, no external DB, no .env file needed.
#
# Usage: ./start.sh [port]
#   port defaults to 18080

set -e
cd "$(dirname "$0")"

PORT="${1:-18080}"
WEB_DIR="apps/web/dist"

# Build frontend if not already built
if [ ! -d "$WEB_DIR" ]; then
  echo "Building frontend (first time)..."
  (cd apps/web && pnpm install --frozen-lockfile && pnpm build)
fi

export PARSAR_FEISHU_MOCK=true
export PARSAR_AGENT_DAEMON_OWNER_URL="http://127.0.0.1:${PORT}"
export PARSAR_ADDR=":${PORT}"
export PARSAR_PUBLIC_URL="http://127.0.0.1:${PORT}"
export PARSAR_WEB_DIST="$PWD/$WEB_DIR"

echo "Starting Parsar on http://127.0.0.1:${PORT}"
echo "  Data dir: ~/.parsar"
echo "  DB: embedded PostgreSQL (auto-managed)"
echo "  Web UI: $PARSAR_WEB_DIST"
echo ""

exec go run ./server/cmd/server
