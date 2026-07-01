# Local-only sandbox image for the docker-backed SandboxProvider.
#
# Unlike the sibling ../parsar-daemon-claudecode/e2b.Dockerfile (amd64,
# parsar-daemon pulled from GitHub Releases), this copies host-built
# linux/arm64 binaries so it runs natively under Docker Desktop on Apple
# Silicon. The binaries and hooks are staged into the build context by
# build.sh — do NOT `docker build` this file directly; run build.sh.
#
# Wired via: AGENT_DAEMON_SANDBOX_BACKEND=docker
#            AGENT_DAEMON_SANDBOX_DOCKER_IMAGE=parsar-sandbox:local
FROM e2bdev/base:latest

RUN apt-get update -y \
 && apt-get install -y --no-install-recommends curl ca-certificates jq \
 && rm -rf /var/lib/apt/lists/*

# Claude Code CLI — the daemon's connect preflight refuses to register
# unless at least one supported agent CLI is on PATH.
RUN curl -fsSL https://claude.ai/install.sh | bash \
 && CLAUDE_BIN="$(command -v claude || true)" \
 && if [ -z "$CLAUDE_BIN" ] && [ -x /root/.local/bin/claude ]; then CLAUDE_BIN=/root/.local/bin/claude; fi \
 && if [ -z "$CLAUDE_BIN" ]; then echo "claude install failed: no binary on PATH"; exit 1; fi \
 && ln -sf "$CLAUDE_BIN" /usr/local/bin/claude \
 && /usr/local/bin/claude --version

COPY parsar-daemon /usr/local/bin/parsar-daemon
COPY parsar /usr/local/bin/parsar
RUN chmod +x /usr/local/bin/parsar-daemon /usr/local/bin/parsar \
 && /usr/local/bin/parsar-daemon version \
 && /usr/local/bin/parsar --version

COPY hooks /opt/parsar/hooks
RUN chmod -R a+rx /opt/parsar/hooks

ENV IS_SANDBOX=1
ENV DISABLE_TELEMETRY=1
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

RUN mkdir -p /workspace
WORKDIR /workspace
