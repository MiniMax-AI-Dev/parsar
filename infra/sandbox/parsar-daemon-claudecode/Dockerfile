# Parsar agent_daemon sandbox image — envd + Claude Code CLI + parsar-daemon
#
# Contents:
#   - envd (e2b sandbox daemon) → /usr/bin/envd
#   - Claude Code CLI           → /usr/local/bin/claude
#   - parsar-daemon binary          → /usr/local/bin/parsar-daemon
#   - MCP server runtimes       → Node 20 (npx) + Python 3 + uv (uvx)
#   - Basic utilities           → curl, jq, git, ripgrep
#
# NOTE: envd MUST be baked into the image. The container Command
# (["envd", "-port", "49983"]) fails with "executable file not found in
# $PATH" → CrashLoopBackOff otherwise. We pull envd from the public
# `e2bdev/base` image instead of relying on cluster-side init injection
# (some SandboxSet controllers do not honour `should-init-envd`).
#
# Build:
#   docker build -t ghcr.io/<your-org>/parsar-sandbox:<tag> .
#
# Push:
#   docker push ghcr.io/<your-org>/parsar-sandbox:<tag>
#
# CI (.github/workflows/sandbox-image-release.yml) builds and pushes the
# image to ghcr.io on every change under infra/sandbox/. Operators who
# want their own registry should override the `image:` field in
# sandboxset.yaml + the `<your-org>` placeholder above.
#
# Build args:
#   --build-arg PARSAR_DAEMON_REPO=<owner>/<repo>   override parsar-daemon source repo
#   --build-arg PARSAR_DAEMON_VERSION=<release-tag> pin parsar-daemon to a specific release
#   --build-arg CLAUDE_CODE_VERSION=<version>   pin claude-code (default: latest)
#   --build-arg CODEX_VERSION=<version>         pin codex-rs (default: 0.141.0)

FROM ubuntu:22.04

# --- envd binary (e2b sandbox daemon) ---
# Bake envd into the image so the SandboxSet container Command resolves.
# Pulled from the public e2b base image.
COPY --from=e2bdev/base:latest --chmod=755 /usr/bin/envd /usr/bin/envd

# --- Base tools ---
# curl/ca-certificates for downloads, jq for JSON, git for repo ops,
# ripgrep for Claude Code's code search, plus basic coreutils the
# Claude Code subprocess may shell out to.
RUN apt-get update -y \
 && apt-get install -y --no-install-recommends \
      curl ca-certificates jq git ripgrep \
 && rm -rf /var/lib/apt/lists/*

# --- MCP server runtimes (Node + Python + uv) ---
# Most community MCP servers in canonical_spec.mcp.servers[].command are
# launched via `npx <package>` (Node ecosystem) or `uvx <package>` (Python
# ecosystem). Without these runtimes pre-installed, Claude Code's
# --mcp-config tempfile entries fail with `spawn npx ENOENT` / `spawn uvx
# ENOENT` AFTER the server-side capability resolution has already logged
# "mcp_server_count=1" — silent runtime failure with no UI signal.
# Pre-bake both runtimes so any canonical MCP binding "just works" once
# the user supplies the credential.
#
# Choices:
#  - Node 20 LTS via NodeSource: distro package (apt's nodejs) on
#    ubuntu:22.04 is 12.x — too old for most @modelcontextprotocol/*
#    packages.
#  - python3 from distro: needed because some uvx-launched MCP servers
#    pull in Python deps that link against the system Python at runtime.
#  - uv installed via the official `astral.sh/uv/install.sh` script
#    (NOT via pip): pip on Ubuntu 22.04 is 22.0.2 which does not yet
#    accept `--break-system-packages`, and even if it did, the uv folks
#    explicitly recommend their installer script over pip because it
#    drops a single static binary in /root/.local/bin (no Python
#    interpreter coupling). Symlink uv + uvx into /usr/local/bin so
#    `exec.LookPath("uvx")` from Claude Code's MCP spawner resolves.
#
# Size impact: ~150MB (Node ~90MB + Python tooling + uv ~60MB). Acceptable
# trade for eliminating the entire silent-failure class.
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
 && apt-get install -y --no-install-recommends nodejs python3 \
 && rm -rf /var/lib/apt/lists/* \
 && curl -LsSf https://astral.sh/uv/install.sh | sh \
 && ln -sf /root/.local/bin/uv /usr/local/bin/uv \
 && ln -sf /root/.local/bin/uvx /usr/local/bin/uvx \
 && node --version \
 && npx --version \
 && python3 --version \
 && uvx --version

# --- Claude Code CLI ---
# Pulled directly from Anthropic's `downloads.claude.ai/claude-code-releases`
# CDN. We used to call `https://claude.ai/install.sh | bash` but that URL
# now sits behind Cloudflare's managed bot challenge (returns 403 for any
# non-browser User-Agent), so curl from `docker build` fails:
#
#   curl: (22) The requested URL returned error: 403
#
# The CDN endpoint that install.sh hits internally is NOT challenged, so
# we replicate its three steps inline:
#   1. GET /latest                  → resolves to a version string (or use
#                                     the explicit CLAUDE_CODE_VERSION arg
#                                     to pin, e.g. for a bisect)
#   2. GET /<version>/linux-x64/claude → downloads the static binary
#   3. chmod + place on PATH
#
# To pin (e.g. for a bisect):
#   docker build --build-arg CLAUDE_CODE_VERSION=2.1.179 ...
ARG CLAUDE_CODE_VERSION=""
RUN set -e; \
    DOWNLOAD_BASE="https://downloads.claude.ai/claude-code-releases"; \
    VERSION="${CLAUDE_CODE_VERSION}"; \
    if [ -z "$VERSION" ]; then \
      VERSION="$(curl -fsSL "$DOWNLOAD_BASE/latest")"; \
      [ -n "$VERSION" ] || { echo "claude install: failed to resolve latest version" >&2; exit 1; }; \
    fi; \
    echo "Installing claude code ${VERSION}"; \
    mkdir -p /root/.local/bin; \
    curl -fsSL "$DOWNLOAD_BASE/${VERSION}/linux-x64/claude" -o /root/.local/bin/claude; \
    chmod +x /root/.local/bin/claude; \
    ln -sf /root/.local/bin/claude /usr/local/bin/claude; \
    /usr/local/bin/claude --version

# --- parsar-daemon static binary ---
# Pulled from GitHub Releases. The repo and version are build args so
# forks can point at their own release stream.
#
# PARSAR_DAEMON_REPO=<owner>/<name>   default `MiniMax-AI-Dev/parsar`
# PARSAR_DAEMON_VERSION=<release-tag> default resolves `releases/latest`
#                                 (format: `parsar-daemon-v0.1.0` per the
#                                  release workflow).
ARG PARSAR_DAEMON_REPO=MiniMax-AI-Dev/parsar
ARG PARSAR_DAEMON_VERSION=""
RUN set -e; \
    REPO="${PARSAR_DAEMON_REPO}"; \
    VERSION="${PARSAR_DAEMON_VERSION}"; \
    if [ -z "$VERSION" ]; then \
      LATEST_URL="$(curl -fsILo /dev/null -w '%{url_effective}' \
        "https://github.com/${REPO}/releases/latest")"; \
      VERSION="${LATEST_URL##*/tag/}"; \
      [ -n "$VERSION" ] && [ "$VERSION" != "$LATEST_URL" ] \
        || { echo "Failed to resolve latest parsar-daemon release from ${REPO}" >&2; exit 1; }; \
    fi; \
    BARE_VERSION="${VERSION#parsar-daemon-}"; \
    BINARY="parsar-daemon-${BARE_VERSION}-linux-amd64"; \
    echo "Installing parsar-daemon ${VERSION} (${BINARY}) from github.com/${REPO}"; \
    curl -fsSL \
      "https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}" \
      -o /usr/local/bin/parsar-daemon \
 && chmod +x /usr/local/bin/parsar-daemon \
 && /usr/local/bin/parsar-daemon version

# --- OpenAI Codex CLI (codex-rs) ---
# Downloaded directly from the openai/codex GitHub release tarball. The
# linux-musl build is statically linked, so no glibc / libc++ dependency
# is added to the image.
#
# Default pinned to keep image builds reproducible: an empty CODEX_VERSION
# would otherwise resolve the GitHub `latest` tag at build time, which
# drifts whenever upstream cuts a release. Bump this when you want the
# sandbox to pick up a new codex-rs (and bisect with --build-arg if you
# need to confirm a regression came from the pin bump itself).
#
# Override: docker build --build-arg CODEX_VERSION=0.142.0 ...
ARG CODEX_VERSION="0.141.0"
RUN set -e; \
    VERSION="${CODEX_VERSION}"; \
    if [ -z "$VERSION" ]; then \
      VERSION="$(curl -fsSL https://api.github.com/repos/openai/codex/releases/latest \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"rust-v\([^"]*\)".*/\1/p' | head -1)"; \
      [ -n "$VERSION" ] || { echo "Failed to resolve latest codex version" >&2; exit 1; }; \
    fi; \
    echo "Installing codex ${VERSION}"; \
    curl -fsSL \
      "https://github.com/openai/codex/releases/download/rust-v${VERSION}/codex-x86_64-unknown-linux-musl.tar.gz" \
      -o /tmp/codex.tar.gz; \
    tar -xzf /tmp/codex.tar.gz -C /tmp/; \
    install -m 0755 /tmp/codex-x86_64-unknown-linux-musl /usr/local/bin/codex; \
    rm -rf /tmp/codex*; \
    /usr/local/bin/codex --version

# --- Sandbox environment ---
ENV IS_SANDBOX=1
ENV DISABLE_TELEMETRY=1
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

RUN mkdir -p /workspace
WORKDIR /workspace
