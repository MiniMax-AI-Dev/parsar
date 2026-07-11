# Parsar E2B-compatible sandbox image.
#
#   docker build -f infra/sandbox/Dockerfile \
#     --build-arg BASE_IMAGE=e2bdev/base:latest -t parsar-sandbox-e2b .
#
# After build, the provider creates the template, runs
# `parsar-daemon connect --device-name <deviceId> -b` with URL/token in env,
# and waits for the gateway to see that device connect.
#
# Contents:
#   - Claude Code CLI + Codex CLI + Pi CLI (installed by
#     infra/sandbox/scripts/install-agents.sh)
#   - parsar-daemon + parsar CLI (spec/memory hook injection), both
#     compiled from source in the builder stage below — no dependency on
#     a published parsar-daemon release or the parsar:local server image
#   - Claude Code hook scripts (session-start / user-prompt-submit)
#   - Python 3 + uv for MCP server runtimes (Node comes from
#     install-agents.sh)
#
# Deliberately NOT included: OpenCode. Add it back (`npm install -g
# opencode-ai`) if a sandbox needs to run agent_kind=opencode.
#
# Build args:
#   --build-arg BASE_IMAGE=<image>             ubuntu:22.04 (local) or e2bdev/base:latest (e2b)
#   --build-arg GO_VERSION=<tag>               builder Go image (default: matches repo go.work)
#   --build-arg CLAUDE_CODE_VERSION=<version>  pin claude code (default: latest)
#   --build-arg CODEX_VERSION=<version>        pin codex-rs (default: 0.141.0)
#   --build-arg PI_VERSION=<version>           pin pi CLI (default: 0.80.6)

###############################################################################
# Stage 1: go-builder — compile parsar-daemon + parsar CLI for TARGETARCH.
# --platform=$BUILDPLATFORM keeps the Go toolchain itself running natively
# (no QEMU) while GOARCH=$TARGETARCH cross-compiles the output binaries.
###############################################################################
ARG GO_VERSION=1.25-bookworm
ARG BASE_IMAGE=ubuntu:22.04

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS go-builder
ARG TARGETARCH
WORKDIR /src

# Module graph first, source second — keeps `go mod download` cacheable.
COPY go.mod go.sum go.work ./
COPY go.work.sum* ./
RUN go mod download

# parsar-daemon depends on the repo-root internal/ packages (single
# go.work module graph, no separate go.mod under apps/).
COPY internal ./internal
COPY apps ./apps

# Hook scripts call `parsar inject snapshot/incremental` to fetch
# spec+memory from the server — without this binary, hooks fail open and
# the agent boots without workspace context.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} GOFLAGS=-trimpath \
    go build -ldflags="-s -w" -o /out/parsar-daemon ./apps/parsar-daemon/cmd/parsar-daemon
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} GOFLAGS=-trimpath \
    go build -ldflags="-s -w -X github.com/MiniMax-AI-Dev/parsar/apps/parsar/internal/cli.Version=sandbox" \
    -o /out/parsar ./apps/parsar/cmd/parsar

###############################################################################
# Stage 2: runtime — the actual sandbox image.
###############################################################################
FROM ${BASE_IMAGE}
# Bare, no default: BuildKit auto-populates this per target platform for
# every multi-platform build graph node. A hardcoded default here (we
# used to have =amd64, reasoning "e2b is amd64-only anyway") SHADOWS
# that auto-injection instead of just being a fallback — it silently
# forced every platform, including arm64 build graph nodes, to resolve
# TARGETARCH=amd64, downloading x86_64 CLI binaries onto an aarch64
# rootfs (fails at exec time: "No such file or directory", the classic
# symptom of a missing ELF interpreter for the wrong architecture).
ARG TARGETARCH

# --- Base tools ---
# curl/ca-certificates for downloads, jq for JSON, git for repo ops,
# ripgrep for Claude Code's code search.
RUN apt-get update -y \
 && apt-get install -y --no-install-recommends \
      curl ca-certificates jq git ripgrep \
 && rm -rf /var/lib/apt/lists/*

# --- Agent CLIs (Node + Claude Code + Codex + Pi) ---
# See infra/sandbox/scripts/install-agents.sh for the install logic and
# version-pin build args.
ARG CLAUDE_CODE_VERSION=""
ARG CODEX_VERSION="0.141.0"
ARG PI_VERSION="0.80.6"
COPY infra/sandbox/scripts/install-agents.sh /tmp/install-agents.sh
RUN CLAUDE_CODE_VERSION="$CLAUDE_CODE_VERSION" CODEX_VERSION="$CODEX_VERSION" PI_VERSION="$PI_VERSION" \
    /tmp/install-agents.sh "$TARGETARCH" \
 && rm -f /tmp/install-agents.sh

# --- Python 3 + uv (MCP server runtimes) ---
# Most community MCP servers are launched via `npx <package>` or `uvx
# <package>`; without these pre-installed, --mcp-config entries fail with
# `spawn npx/uvx ENOENT` after capability resolution already logged
# success — a silent runtime failure with no UI signal. npx already
# resolves from install-agents.sh's Node install.
RUN apt-get update -y \
 && apt-get install -y --no-install-recommends python3 \
 && rm -rf /var/lib/apt/lists/* \
 && curl -LsSf https://astral.sh/uv/install.sh | sh \
 && ln -sf /root/.local/bin/uv /usr/local/bin/uv \
 && ln -sf /root/.local/bin/uvx /usr/local/bin/uvx \
 && python3 --version \
 && uvx --version

# --- parsar-daemon + parsar CLI (compiled in the go-builder stage) ---
COPY --from=go-builder /out/parsar-daemon /usr/local/bin/parsar-daemon
COPY --from=go-builder /out/parsar /usr/local/bin/parsar
RUN chmod +x /usr/local/bin/parsar-daemon /usr/local/bin/parsar \
 && /usr/local/bin/parsar-daemon version \
 && /usr/local/bin/parsar --version

# --- Hook scripts (Claude Code) ---
# Pre-baked under /opt/parsar/hooks/claude/. Hook scripts call `parsar
# inject snapshot/incremental` at runtime. settings.json pointing at
# these scripts is written per-sandbox at seed time, not baked here.
COPY infra/sandbox/hooks/claude /opt/parsar/hooks/claude
RUN chmod -R a+rx /opt/parsar/hooks

# --- Sandbox environment ---
ENV IS_SANDBOX=1
ENV DISABLE_TELEMETRY=1
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

RUN mkdir -p /workspace
WORKDIR /workspace
