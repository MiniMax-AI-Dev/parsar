#!/usr/bin/env bash
# Tail the Parsar dev server log.
# Pass --follow / -f to keep streaming; default is the last 100 lines.
set -euo pipefail

LOG_PATH="${HOME}/.parsar/logs/server.log"

if [[ ! -f "${LOG_PATH}" ]]; then
  echo "[dev-server-log] no log at ${LOG_PATH} — has the server ever run?" >&2
  echo "                start it with: scripts/dev-server-up.sh" >&2
  exit 1
fi

case "${1:-}" in
  -f|--follow) tail -F "${LOG_PATH}" ;;
  *)           tail -100 "${LOG_PATH}" ;;
esac
