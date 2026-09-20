# Build context is a prepared binary bundle, never the product checkout.
# Native package and resources must both be Codex 0.153.4 (linux/amd64).
FROM debian:bookworm-slim@sha256:a0982977ea1cf754c15281f48a7d5957cd14039a2a4f2aca9f23d74d226003d0
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 python3-pip nodejs npm ripgrep bubblewrap \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime \
    && groupadd --gid 1000 runtime \
    && useradd --uid 1000 --gid 1000 --home-dir /home/runtime --no-create-home runtime
COPY build-system-seed.py /tmp/build-system-seed.py
RUN python3 -I -S /tmp/build-system-seed.py && rm /tmp/build-system-seed.py
COPY --chmod=0555 tool-root.py /usr/local/bin/agents-api-tool-root
COPY --chmod=0555 parsar-daemon agents-api-codex-directory agents-api-codex-write agents-api-workspace-export codex /usr/local/bin/
COPY codex-resources /usr/local/codex-resources
COPY requirements.toml /etc/codex/requirements.toml
COPY --chmod=0444 tool-env.py /etc/codex/tool-env.py
RUN chmod 0555 /etc/codex && chmod 0444 /etc/codex/requirements.toml
COPY --chmod=0444 runtime-initialize.py /usr/local/bin/agents-api-runtime-initialize
ENV HOME=/home/runtime PARSAR_HOME=/home/runtime/.parsar \
    PARSAR_CODEX_BIN=/usr/local/bin/codex PARSAR_CODEX_PERMISSION_PROFILE=managed-workspace \
    PARSAR_RUNTIME_WORKSPACE=/environment/workspace \
    PARSAR_RUNTIME_DIRECTORY_HELPER=/usr/local/bin/agents-api-codex-directory \
    PARSAR_RUNTIME_WRITE_HELPER=/usr/local/bin/agents-api-codex-write \
    PARSAR_RUNTIME_EXPORT_HELPER=/usr/local/bin/agents-api-workspace-export \
    PARSAR_RUNTIME_STAGING=/environment/staging
USER 1000:1000
RUN test -r /etc/codex/requirements.toml && test "$(codex --version)" = "codex-cli 0.153.4"
WORKDIR /environment/workspace
ENTRYPOINT ["/usr/local/bin/parsar-daemon"]
CMD ["connect", "--profile", "default"]
