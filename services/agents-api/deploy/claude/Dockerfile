# Build context contains only the daemon, shared helpers and pinned SDK bundle.
FROM node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 ripgrep bubblewrap socat \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime
COPY --chmod=0555 parsar-daemon agents-api-codex-directory agents-api-codex-write agents-api-workspace-export /usr/local/bin/
COPY claude-sdk /opt/claude-sdk
ENV HOME=/home/runtime PARSAR_HOME=/home/runtime/.parsar \
    PARSAR_CLAUDE_SDK_NODE=/usr/local/bin/node \
    PARSAR_CLAUDE_SDK_ENTRYPOINT=/opt/claude-sdk/dist/main.js \
    PARSAR_CLAUDE_SDK_WORKSPACE=managed \
    PARSAR_RUNTIME_WORKSPACE=/environment/workspace \
    PARSAR_RUNTIME_DIRECTORY_HELPER=/usr/local/bin/agents-api-codex-directory \
    PARSAR_RUNTIME_WRITE_HELPER=/usr/local/bin/agents-api-codex-write \
    PARSAR_RUNTIME_EXPORT_HELPER=/usr/local/bin/agents-api-workspace-export \
    PARSAR_RUNTIME_STAGING=/environment/staging
USER 1000:1000
RUN node /opt/claude-sdk/dist/runtime_check.js /opt/claude-sdk/dist/main.js
WORKDIR /environment/workspace
ENTRYPOINT ["/usr/local/bin/parsar-daemon"]
CMD ["connect", "--profile", "default"]
