#!/usr/bin/env bash
# parsar-daemon-e2e.sh — manual end-to-end walkthrough for §11.1 of
# the daemon plan. Builds the parsar-daemon binary for the current
# platform, sanity-checks prerequisites, and prints the exact
# steps to reproduce the PR3 acceptance scenario:
#
#   pair token (web)  →  parsar-daemon connect --url --token  →
#   web prompt  →  streamed delta  →  Bash permission prompt  →
#   approve  →  result.
#
# Why "walkthrough" rather than full automation: the flow
# requires (1) clicking through the Runtime → Agent Daemon tab
# to mint a pairing token, (2) Claude CLI auth on the user's
# machine, and (3) watching for SSE deltas in the browser. Each
# of those is faster to do by hand than to script; the script's
# job is to remove the boilerplate (build, prereq checks,
# command echoing) so the reviewer can focus on the parts a
# human has to do anyway.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

DAEMON_BIN_DIR="$ROOT_DIR/apps/parsar-daemon/bin"
DAEMON_BIN="$DAEMON_BIN_DIR/parsar-daemon"

bold()  { printf '\033[1m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
yellow(){ printf '\033[33m%s\033[0m\n' "$*"; }
red()   { printf '\033[31m%s\033[0m\n' "$*" >&2; }

bold "==> Step 1/4: prerequisite check"

if ! command -v go >/dev/null 2>&1; then
  red "go toolchain not on PATH — install Go 1.22+ and retry."
  exit 1
fi
green "  go: $(go version)"

if ! command -v claude >/dev/null 2>&1; then
  yellow "  claude CLI missing on PATH."
  yellow "  parsar-daemon connect will fail its preflight until you install it:"
  yellow "    https://docs.anthropic.com/claude/docs/claude-code"
  yellow "  Continuing anyway so you can still build + sanity-check the binary."
else
  green "  claude: $(claude --version 2>&1 | head -n1)"
fi

if ! command -v pnpm >/dev/null 2>&1; then
  yellow "  pnpm missing — you won't be able to start the web UI side."
fi

bold "==> Step 2/4: build parsar-daemon for the current platform"

mkdir -p "$DAEMON_BIN_DIR"
( cd "$ROOT_DIR/apps/parsar-daemon" && go build -o "$DAEMON_BIN" ./cmd/parsar-daemon )
green "  binary: $DAEMON_BIN"
"$DAEMON_BIN" --help >/dev/null 2>&1 || true  # warmup so first ttyless run isn't slow

bold "==> Step 3/4: confirm dev server is reachable"

SERVER_URL="${PARSAR_DAEMON_E2E_SERVER:-http://127.0.0.1:3000}"
if curl -fsS "${SERVER_URL%/}/healthz" >/dev/null 2>&1; then
  green "  dev server up at $SERVER_URL"
elif curl -fsS "${SERVER_URL%/}/api/v1/me" >/dev/null 2>&1; then
  # Some envs don't expose /healthz publicly; /api/v1/me at least
  # tells us the HTTP listener is alive.
  green "  dev server up at $SERVER_URL (no /healthz, but /api/v1/me responds)"
else
  yellow "  $SERVER_URL is not reachable."
  yellow "  Start it in another shell:  ./scripts/dev-server-up.sh"
  yellow "  Then re-run this script."
fi

bold "==> Step 4/4: manual reproduction steps"
cat <<EOF

Follow these steps in order. Each block can be re-run independently
if something goes wrong mid-way.

  ┌─ A. Mint a pairing token (browser) ─────────────────────────────
  │   1. Open  $SERVER_URL/admin?admin=runtime
  │   2. Click the "Agent Daemon" tab.
  │   3. Click "Pair new device".
  │   4. Enter a device name (e.g. "$(hostname -s 2>/dev/null || echo dev-mac)").
  │   5. Click "Generate pairing command" — copy the command shown.
  └─────────────────────────────────────────────────────────────────

  ┌─ B. Connect this machine ───────────────────────────────────────
  │   $DAEMON_BIN connect \
  │       --url $SERVER_URL \
  │       --token <PASTE-TOKEN-FROM-STEP-A> \
  │       --device-name $(hostname -s 2>/dev/null || echo dev-mac)
  │
  │   Expected log line: "ws connected" — the runtime row in the
  │   web tab should flip to "online" within ~5s.
  │
  │   If you want a backgrounded process instead, add -b to the same
  │   command and then watch logs:
  │       $DAEMON_BIN logs -f
  │   Stop it with: $DAEMON_BIN stop
  └─────────────────────────────────────────────────────────────────

  ┌─ C. Drive a prompt from the web ────────────────────────────────
  │   1. Open  $SERVER_URL/admin?admin=agents
  │   2. New Agent → connector_type = agent_daemon.
  │   3. In the "Agent Daemon device" field, pick the device you
  │      paired in step A.
  │   4. Save, open a conversation, send a prompt like:
  │        "list the files in this directory"
  │   5. Watch for:
  │        - streamed delta events arriving in the chat
  │        - a Bash permission request popping up in the UI
  │        - approve it → the command runs, output streams back
  │        - "done" event closes the run
  └─────────────────────────────────────────────────────────────────

  ┌─ D. (optional) Cancellation + reconnect smoke ──────────────────
  │   Cancel: long-running prompt → click cancel in UI → expect the
  │     run to settle with error "cancelled" then done.
  │   Reconnect: pkill -STOP -f parsar-daemon; wait 60s for the runtime
  │     row to flag offline; pkill -CONT -f parsar-daemon; row should
  │     return to "online" within ~15s.
  └─────────────────────────────────────────────────────────────────

EOF

green "Walkthrough printed. Happy testing."
