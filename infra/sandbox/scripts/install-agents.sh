#!/usr/bin/env bash
# Installs every agent CLI a Parsar sandbox image needs: Node 22 (for npm-
# based installs), Claude Code, Codex, and Pi. Used by
# infra/sandbox/Dockerfile (both the local-docker and e2b.app build
# targets, selected by --build-arg BASE_IMAGE). Edit here, not inline in
# the Dockerfile.
#
# Usage: install-agents.sh <amd64|arm64>
# Version overrides (env, all optional):
#   CLAUDE_CODE_VERSION   default: latest
#   CODEX_VERSION         default: 0.141.0
#   PI_VERSION            default: 0.80.6
#
# All installs are FAIL-LOUD: `set -e` + a `--version` sanity check after
# each one. A silently missing CLI would only surface at run time when a
# user creates an Agent with the corresponding agent_kind and the sandbox
# reports "command not found".
set -euo pipefail

TARGETARCH="${1:?install-agents.sh: TARGETARCH required (amd64|arm64)}"
CLAUDE_CODE_VERSION="${CLAUDE_CODE_VERSION:-}"
CODEX_VERSION="${CODEX_VERSION:-0.141.0}"
PI_VERSION="${PI_VERSION:-0.80.6}"

case "$TARGETARCH" in
  amd64) CLAUDE_ARCH=linux-x64 CODEX_ARCH=x86_64-unknown-linux-musl ;;
  arm64) CLAUDE_ARCH=linux-arm64 CODEX_ARCH=aarch64-unknown-linux-musl ;;
  *) echo "install-agents: unsupported TARGETARCH=$TARGETARCH" >&2; exit 1 ;;
esac

# --- Node 22 LTS via NodeSource ---
# Distro nodejs packages on ubuntu:22.04 / debian:12 are 12.x-18.x — too
# old for most current npm packages. Node 22, not 20: @earendil-works/
# pi-coding-agent pins engines.node >=22.19.0 and fails at *runtime* (not
# just an npm EBADENGINE warning) on 20.x — TypeError:
# webidl.util.markAsUncloneable is not a function, from undici's
# Node-22-only Cache API surface.
#
# Force-relink /usr/local/bin: e2bdev/base ships its own /usr/local/bin/
# node (v20.9.0) baked into the image, and /usr/local/bin wins over the
# nodesource package's /usr/bin/node on PATH. `command -v node` after
# install still resolves the stale /usr/local/bin one (that's the whole
# problem), so this points directly at dpkg's actual install path rather
# than trusting PATH resolution — without this every npm-installed CLI
# silently runs on the stale bundled Node instead of the one we just
# installed.
echo "install-agents: installing Node 22"
curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
apt-get install -y --no-install-recommends nodejs
rm -rf /var/lib/apt/lists/*
ln -sf /usr/bin/node /usr/local/bin/node
ln -sf /usr/bin/npm /usr/local/bin/npm
ln -sf /usr/bin/npx /usr/local/bin/npx
hash -r
node --version
npx --version

# --- Claude Code CLI ---
# Pulled from Anthropic's downloads CDN, not claude.ai/install.sh — the
# latter sits behind a bot challenge that 403s non-browser User-Agents.
echo "install-agents: installing claude code"
DOWNLOAD_BASE="https://downloads.claude.ai/claude-code-releases"
VERSION="$CLAUDE_CODE_VERSION"
if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "$DOWNLOAD_BASE/latest")"
  [ -n "$VERSION" ] || { echo "claude install: failed to resolve latest version" >&2; exit 1; }
fi
mkdir -p /root/.local/bin
curl -fsSL "$DOWNLOAD_BASE/${VERSION}/${CLAUDE_ARCH}/claude" -o /root/.local/bin/claude
chmod +x /root/.local/bin/claude
ln -sf /root/.local/bin/claude /usr/local/bin/claude
/usr/local/bin/claude --version

# --- OpenAI Codex CLI (codex-rs) ---
# linux-musl builds are statically linked, no glibc dependency added.
# Pinned by default for reproducible builds; bump deliberately.
echo "install-agents: installing codex ${CODEX_VERSION}"
curl -fsSL \
  "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-${CODEX_ARCH}.tar.gz" \
  -o /tmp/codex.tar.gz
tar -xzf /tmp/codex.tar.gz -C /tmp/
install -m 0755 "/tmp/codex-${CODEX_ARCH}" /usr/local/bin/codex
rm -rf /tmp/codex*
/usr/local/bin/codex --version

# --- Pi CLI (via npm) ---
echo "install-agents: installing pi ${PI_VERSION}"
npm install -g "@earendil-works/pi-coding-agent@${PI_VERSION}"
pi --version
