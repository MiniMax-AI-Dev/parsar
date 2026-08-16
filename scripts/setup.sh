#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   setup.sh         create ~/.parsar dirs, then print resolved paths
#   setup.sh paths   print resolved paths only, no directory creation
MODE="${1:-setup}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/paths.sh
source "$SCRIPT_DIR/lib/paths.sh"

if [[ "$MODE" == "setup" ]]; then
  mkdir -p \
    "$PARSAR_CONFIG_DIR" \
    "$PARSAR_LOG_DIR" \
    "$PARSAR_STATE_DIR" \
    "$PARSAR_CACHE_DIR" \
    "$PARSAR_DEV_DIR/tasks" \
    "$PARSAR_DEV_DIR/opencode/config" \
    "$PARSAR_DEV_DIR/opencode/data" \
    "$PARSAR_DEV_DIR/opencode/logs" \
    "$PARSAR_DEV_DIR/workdirs"

  printf 'Parsar directories are ready under %s\n' "$PARSAR_HOME"
elif [[ "$MODE" != "paths" ]]; then
  printf 'Unknown mode: %s (expected "setup" or "paths")\n' "$MODE" >&2
  exit 1
fi

cat <<PATHS
PARSAR_HOME=$PARSAR_HOME
PARSAR_CONFIG_DIR=$PARSAR_CONFIG_DIR
PARSAR_LOG_DIR=$PARSAR_LOG_DIR
PARSAR_STATE_DIR=$PARSAR_STATE_DIR
PARSAR_CACHE_DIR=$PARSAR_CACHE_DIR
PARSAR_DEV_DIR=$PARSAR_DEV_DIR
PATHS
