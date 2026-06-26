#!/usr/bin/env bash
# Stop the Parsar dev server tmux session.
# See scripts/dev-server-up.sh for the rationale.
set -euo pipefail

TMUX_SESSION="parsar-server"
BIN_PATH="${HOME}/.parsar/bin/parsar-server"

if tmux has-session -t "${TMUX_SESSION}" 2>/dev/null; then
  tmux kill-session -t "${TMUX_SESSION}"
  echo "[dev-server-down] killed tmux session '${TMUX_SESSION}'"
else
  echo "[dev-server-down] tmux session '${TMUX_SESSION}' not running"
fi

# Best-effort kill of orphaned binaries (e.g. if tmux died but the
# server kept running because the heredoc detached it).
if pkill -f "${BIN_PATH}" 2>/dev/null; then
  echo "[dev-server-down] killed lingering '${BIN_PATH}' processes"
fi
