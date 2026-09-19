# Build context contains the daemon, shared helpers, published CLI and tool companion.
FROM node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 ripgrep bubblewrap socat \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime
COPY --chmod=0555 parsar-daemon agents-api-codex-directory agents-api-codex-write agents-api-workspace-export /usr/local/bin/
COPY mcode /opt/mcode
COPY mcode-harness /opt/mcode-harness
ENV HOME=/home/runtime PARSAR_HOME=/home/runtime/.parsar \
    PARSAR_MCODE_NODE=/usr/local/bin/node \
    PARSAR_MCODE_BIN=/opt/mcode/cli.js \
    PARSAR_MCODE_WORKSPACE_BRIDGE=/opt/mcode-harness/bridge.mjs \
    PARSAR_MCODE_AGENTS_API=1 \
    PARSAR_MCODE_WORKSPACE=managed \
    PARSAR_RUNTIME_WORKSPACE=/environment/workspace \
    PARSAR_RUNTIME_DIRECTORY_HELPER=/usr/local/bin/agents-api-codex-directory \
    PARSAR_RUNTIME_WRITE_HELPER=/usr/local/bin/agents-api-codex-write \
    PARSAR_RUNTIME_EXPORT_HELPER=/usr/local/bin/agents-api-workspace-export \
    PARSAR_RUNTIME_STAGING=/environment/staging
USER 1000:1000
RUN node /opt/mcode-harness/check.mjs && /opt/mcode/cli.js --version
WORKDIR /environment/workspace
ENTRYPOINT ["/usr/local/bin/parsar-daemon"]
CMD ["connect", "--profile", "default"]
