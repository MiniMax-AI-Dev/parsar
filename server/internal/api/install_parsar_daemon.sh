#!/usr/bin/env bash
# Parsar — parsar-daemon installer.
#
# Downloads the platform-appropriate parsar-daemon binary from the project's
# GitHub Releases. Does NOT chmod, NOT install onto $PATH, NOT touch
# system directories — the caller (admin operator on their own laptop)
# decides where the binary lands. We just put it next to the user's
# Downloads so a managed laptop policy can intercept it before exec.
#
# Resolution order (highest priority first):
#   1. $PARSAR_DAEMON_VERSION       — exact GitHub Release tag (e.g. v0.1.0)
#   2. GitHub `releases/latest` — most recent stable release
#
# Repo selection (highest priority first):
#   1. $PARSAR_DAEMON_REPO          — `owner/repo` slug (e.g. MiniMax-AI-Dev/parsar)
#   2. Built-in default below   — placeholder; deployments should override
#
# Usage:
#   curl -fsSL https://<your-parsar-server>/api/v1/parsar-daemon/install.sh | bash
#   curl -fsSL .../install.sh | PARSAR_DAEMON_VERSION=v0.1.0 bash
#   curl -fsSL .../install.sh | PARSAR_DAEMON_REPO=your-org/your-fork bash
#
#   # One-line connect (what the Parsar web UI mints — never run by hand):
#   curl -fsSL .../install.sh \
#     | PARSAR_DAEMON_CONNECT_URL=https://<server> \
#       PARSAR_DAEMON_CONNECT_TOKEN=<one-shot> \
#       PARSAR_DAEMON_CONNECT_DEVICE_NAME=<name> bash
#
# Exit codes: 0 on success, non-zero on any error (set -e).

set -euo pipefail

# The server-side endpoint that serves this script can inject the repo
# slug for the deployment via the placeholder below; if it stays as
# `__PARSAR_DAEMON_REPO__` the literal default kicks in.
DEFAULT_REPO="__PARSAR_DAEMON_REPO__"
case "$DEFAULT_REPO" in
  __PARSAR_DAEMON_REPO__*) DEFAULT_REPO="MiniMax-AI-Dev/parsar" ;;
esac

REPO="${PARSAR_DAEMON_REPO:-$DEFAULT_REPO}"
VERSION="${PARSAR_DAEMON_VERSION:-}"
OUT_DIR="${PARSAR_DAEMON_OUT_DIR:-${HOME}/Downloads}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$OS" in
  darwin|linux) ;;
  *) echo "parsar-daemon: unsupported OS '${OS}'" >&2; exit 1 ;;
esac
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "parsar-daemon: unsupported architecture '${ARCH}'" >&2; exit 1 ;;
esac

# --- Prefer the minting server's own binary (local / self-host) -------
# The one-line connect command the web UI mints always exports
# PARSAR_DAEMON_CONNECT_URL=<this server's origin>, and that same server
# bakes the cross-compiled daemons into its image and serves them at
# /api/v1/parsar-daemon/download. Trying there first lets a pure-local or
# air-gapped self-host install succeed with no published GitHub Release.
# If the server has no binary for this platform (404) we fall through to
# the GitHub Releases path below — same script, source chosen purely on
# what is reachable.
SERVED_LOCALLY=""
SERVER_ORIGIN="${PARSAR_DAEMON_CONNECT_URL:-}"
if [ -n "$SERVER_ORIGIN" ]; then
  SERVER_ORIGIN="${SERVER_ORIGIN%/}"
  OUT_FILE="${OUT_DIR}/parsar-daemon-${OS}-${ARCH}"
  echo "Fetching parsar-daemon for ${OS}/${ARCH} from ${SERVER_ORIGIN}..."
  mkdir -p "$OUT_DIR"
  if curl -fL "${SERVER_ORIGIN}/api/v1/parsar-daemon/download?os=${OS}&arch=${ARCH}" -o "$OUT_FILE"; then
    printf 'Downloaded to %s\n' "$OUT_FILE"
    SERVED_LOCALLY=1
  else
    echo "Parsar server has no prebuilt binary for ${OS}/${ARCH}; falling back to GitHub Releases." >&2
    rm -f "$OUT_FILE"
  fi
fi

# GitHub Releases path — used for managed-laptop installs (no connect URL)
# and whenever the minting server did not serve a binary above.
if [ -z "$SERVED_LOCALLY" ]; then
  if [ -z "$VERSION" ]; then
    echo "Resolving latest parsar-daemon release from github.com/${REPO}..."
    # GitHub redirects /releases/latest to /releases/tag/<tag>; capture
    # the final URL and strip the tag suffix. -sIL follows redirects with
    # HEAD requests so we don't pull the whole release page.
    LATEST_URL="$(curl -fsILo /dev/null -w '%{url_effective}' \
      "https://github.com/${REPO}/releases/latest")"
    VERSION="${LATEST_URL##*/tag/}"
    if [ -z "$VERSION" ] || [ "$VERSION" = "$LATEST_URL" ]; then
      echo "parsar-daemon: could not resolve latest release tag from ${LATEST_URL}" >&2
      exit 1
    fi
  fi

  # Strip the `parsar-daemon-` prefix the release workflow stamps onto the
  # binary filename (parsar-daemon-release.yml `OUTPUT=parsar-daemon-${VERSION}-${GOOS}-${GOARCH}`).
  BARE_VERSION="${VERSION#parsar-daemon-}"
  BINARY="parsar-daemon-${BARE_VERSION}-${OS}-${ARCH}"
  DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}"
  OUT_FILE="${OUT_DIR}/${BINARY}"

  echo "Downloading ${BINARY} from ${DOWNLOAD_URL}..."
  mkdir -p "$OUT_DIR"
  if ! curl -fL "$DOWNLOAD_URL" -o "$OUT_FILE"; then
    cat >&2 <<ERR

parsar-daemon: download failed.
URL: ${DOWNLOAD_URL}

If the release tag does not exist, override with PARSAR_DAEMON_VERSION.
If you forked the repo, override with PARSAR_DAEMON_REPO=<owner>/<name>.
ERR
    exit 1
  fi

  printf 'Downloaded to %s\n' "$OUT_FILE"
fi

# --- One-line connect mode (north star) -------------------------------
# When the Parsar web "Pair new device / copy one command" button mints a
# command, it pipes this script with the pairing env vars set:
#
#   curl -fsSL .../install.sh \
#     | PARSAR_DAEMON_CONNECT_URL=<server> \
#       PARSAR_DAEMON_CONNECT_TOKEN=<one-shot-token> \
#       PARSAR_DAEMON_CONNECT_DEVICE_NAME=<name> bash
#
# In that mode we finish the job: chmod the freshly-downloaded binary and
# hand off to `connect`, so the operator never touches the binary, its
# path, or the token. `connect` hydrates URL/token/device-name from these
# same env vars and scrubs them from child argv, so the one-shot token
# never appears in any process listing. `exec` replaces this shell; the
# `-b` flag then re-spawns the daemon detached and returns.
#
# Without PARSAR_DAEMON_CONNECT_TOKEN we fall through to the download-only
# enterprise posture below (no chmod, no $PATH changes) — same script,
# behaviour gated purely on env. Profile-not-fork applied to the installer.
if [ -n "${PARSAR_DAEMON_CONNECT_TOKEN:-}" ]; then
  if [ -z "${PARSAR_DAEMON_CONNECT_URL:-}" ]; then
    echo "parsar-daemon: PARSAR_DAEMON_CONNECT_TOKEN is set but PARSAR_DAEMON_CONNECT_URL is empty" >&2
    exit 1
  fi
  printf 'Pairing this device with %s ...\n' "$PARSAR_DAEMON_CONNECT_URL"
  chmod +x "$OUT_FILE"
  exec "$OUT_FILE" connect -b
fi

printf 'Parsar does not install or chmod this file. Review it, then move/install it according to your machine policy:\n'
printf '  chmod +x %s\n' "$OUT_FILE"
printf '  mv %s /usr/local/bin/parsar-daemon   # or any directory on your $PATH\n' "$OUT_FILE"
