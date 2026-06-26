#!/usr/bin/env bash
set -euo pipefail

PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"
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
./scripts/parsar-paths.sh
