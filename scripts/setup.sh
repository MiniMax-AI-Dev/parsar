#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   setup.sh          create ~/.parsar dirs, then print resolved paths
#   setup.sh paths     print resolved paths only, no directory creation
MODE="${1:-setup}"

PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"

if [[ "$MODE" == "setup" ]]; then
  mkdir -p \
    "$PARSAR_HOME/config" \
    "$PARSAR_HOME/logs" \
    "$PARSAR_HOME/state" \
    "$PARSAR_HOME/cache" \
    "$PARSAR_HOME/dev/tasks" \
    "$PARSAR_HOME/dev/opencode/config" \
    "$PARSAR_HOME/dev/opencode/data" \
    "$PARSAR_HOME/dev/opencode/logs" \
    "$PARSAR_HOME/dev/workdirs"

  printf 'Parsar directories are ready under %s\n' "$PARSAR_HOME"
elif [[ "$MODE" != "paths" ]]; then
  printf 'Unknown mode: %s (expected "setup" or "paths")\n' "$MODE" >&2
  exit 1
fi

cat <<PATHS
PARSAR_HOME=$PARSAR_HOME
PARSAR_CONFIG_DIR=$PARSAR_HOME/config
PARSAR_LOG_DIR=$PARSAR_HOME/logs
PARSAR_STATE_DIR=$PARSAR_HOME/state
PARSAR_CACHE_DIR=$PARSAR_HOME/cache
PARSAR_DEV_DIR=$PARSAR_HOME/dev
PATHS
