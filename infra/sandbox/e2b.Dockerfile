# Parsar sandbox image — e2b.app template variant.
#
# Why this file exists separately from infra/sandbox/Dockerfile:
# e2b's template builder rejects multi-stage Dockerfiles ("Multi-stage
# Dockerfiles are not supported"), so the two Go binaries cannot be
# compiled in-image. They are cross-compiled on the host into .build/
# and COPY'd in here. Everything else MUST stay behaviourally identical
# to infra/sandbox/Dockerfile.
#
# Agent CLI installs are NOT written inline here on purpose — they are
# delegated to scripts/install-agents.sh, the same script the main
# Dockerfile runs. That script owns the Node 22 force-relink that this
# base image specifically needs (e2bdev/base ships its own
# /usr/local/bin/node v20.9.0 which otherwise shadows the Node 22 we
# install and silently breaks every npm-installed CLI at runtime).
# Do not reintroduce per-CLI `npm install -g` lines here; edit
# scripts/install-agents.sh so both images move together.
#
# Build context is infra/sandbox/ (NOT the repo root) so the upload
# stays small; every COPY source below lives inside this directory.
#
# Build:
#   make e2b-template
# or manually, from repo root:
#   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
#     -o infra/sandbox/.build/parsar-daemon ./apps/parsar-daemon/cmd/parsar-daemon
#   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
#     -o infra/sandbox/.build/parsar ./apps/parsar/cmd/parsar
#   cd infra/sandbox && e2b template create parsar-sandbox \
#     --dockerfile e2b.Dockerfile --memory-mb 4096

FROM e2bdev/base:latest

# e2bdev/base declares `USER user`. The seed step writes Claude config to
# /root/.claude/settings.json and install-agents.sh installs into
# /root/.local/bin, so the whole image — and the daemon the server later
# execs — has to be root with HOME=/root.
USER root
ENV HOME=/root

# e2b templates are amd64-only; there is no BuildKit multi-platform
# graph here to auto-populate TARGETARCH, so it is a plain default.
ARG TARGETARCH=amd64

# --- Base tools ---
# curl/ca-certificates for downloads, jq for JSON, git for repo ops,
# ripgrep for Claude Code's code search.
RUN apt-get update -y \
 && apt-get install -y --no-install-recommends \
      curl ca-certificates jq git ripgrep \
 && rm -rf /var/lib/apt/lists/*

# --- Agent CLIs (Node 22 + Claude Code + Codex + Pi) ---
# Single source of truth shared with infra/sandbox/Dockerfile. The script
# is fail-loud: each install is followed by a --version check, so a broken
# CLI fails the template build instead of surfacing as "command not found"
# when a user first creates an Agent of that agent_kind.
# Version pins are deliberately UNQUOTED. e2b's builder lowers
# `ARG FOO="bar"` to `ENV FOO "bar"` and keeps the quotes as literal
# characters, so a quoted default arrives in the script as `"bar"` and
# corrupts every URL built from it:
#   ARG CODEX_VERSION="0.141.0"  ->  .../rust-v"0.141.0"/...        (404)
#   ARG CLAUDE_CODE_VERSION=""   ->  VERSION='""', which is NOT empty,
#                                    so install-agents.sh skips its
#                                    "resolve latest" branch and requests
#                                    .../claude-code-releases/""/linux-x64/claude
#                                    -> 400 Bad Request.
# CLAUDE_CODE_VERSION is therefore not declared or forwarded at all:
# install-agents.sh treats it as unset and resolves the latest release,
# which is the behaviour we want here anyway.
ARG CODEX_VERSION=0.141.0
ARG PI_VERSION=0.80.6
# Staged under /opt, NOT /tmp: e2b's builder does not persist /tmp across
# layers, so `COPY ... /tmp/x` followed by `RUN /tmp/x` in the next layer
# fails with "No such file or directory". The main Dockerfile can use /tmp
# because BuildKit keeps it; this one cannot.
COPY scripts/install-agents.sh /opt/parsar/bin/install-agents.sh
RUN chmod +x /opt/parsar/bin/install-agents.sh \
 && CODEX_VERSION="$CODEX_VERSION" PI_VERSION="$PI_VERSION" \
    /opt/parsar/bin/install-agents.sh "$TARGETARCH" \
 && rm -f /opt/parsar/bin/install-agents.sh

# --- Python 3 + uv (MCP server runtimes) ---
# Community MCP servers launch via `npx <pkg>` or `uvx <pkg>`; without
# these preinstalled they fail with `spawn npx/uvx ENOENT` *after*
# capability resolution already reported success — a silent runtime
# failure with no UI signal. npx comes from install-agents.sh's Node.
RUN apt-get update -y \
 && apt-get install -y --no-install-recommends python3 \
 && rm -rf /var/lib/apt/lists/* \
 && curl -LsSf https://astral.sh/uv/install.sh | sh \
 && ln -sf /root/.local/bin/uv /usr/local/bin/uv \
 && ln -sf /root/.local/bin/uvx /usr/local/bin/uvx \
 && python3 --version \
 && uvx --version

# --- parsar-daemon + parsar CLI (cross-compiled on the host) ---
# The parsar CLI is what the hook scripts below shell out to for
# `parsar inject snapshot`; without it the hooks fail open and the agent
# boots with no spec/memory context.
COPY .build/parsar-daemon /usr/local/bin/parsar-daemon
COPY .build/parsar /usr/local/bin/parsar
RUN chmod +x /usr/local/bin/parsar-daemon /usr/local/bin/parsar \
 && /usr/local/bin/parsar-daemon version \
 && /usr/local/bin/parsar --version

# --- Hook scripts (Claude Code) ---
# server/internal/connector/agentdaemon/sandbox_seed.go seeds a
# settings.json pointing at these absolute paths
# (/opt/parsar/hooks/claude/{session-start,user-prompt-submit}.py).
# If they are missing the hooks fail open, so spec/memory injection
# degrades silently — the image MUST carry them.
COPY hooks/claude /opt/parsar/hooks/claude
RUN chmod -R a+rx /opt/parsar/hooks

# scripts/runtime-entrypoint.sh is deliberately NOT copied: it is the
# compose-resident runtime's self-pairing loop (shared-token based). On
# the e2b path the server drives `parsar-daemon connect` through envd
# RunCommand instead, so shipping it would be dead weight.

# --- Sandbox environment ---
ENV IS_SANDBOX=1
ENV DISABLE_TELEMETRY=1
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

# /workspace exists for agents whose configured working directory points
# at it; /root/.parsar is the daemon's state root. The connect step falls
# back to /root when an agent has no work_dir configured.
RUN mkdir -p /workspace /root/.parsar
WORKDIR /root
