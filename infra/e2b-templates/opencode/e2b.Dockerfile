# Parsar opencode sandbox template — base image w/ opencode + provider adapters preinstalled
#
# Builds an E2B sandbox image where:
#   - opencode CLI is installed globally via npm (using its native nvm bin/ symlink)
#   - the 4 provider adapters Parsar ships are pre-resolved in npm cache:
#       @ai-sdk/anthropic, @ai-sdk/openai, @ai-sdk/google, @ai-sdk/openai-compatible
#   - Node 22 LTS is on PATH (opencode requires modern Node)
#   - jq + curl are present for opencode.json prep
#
# After build, Parsar BuildSandboxRunner only needs to:
#   1. write opencode.json into the sandbox (per-run model + API key)
#   2. spawn `opencode serve --hostname 0.0.0.0 --port 4096`
# No npm install at spawn time → boot ~3-5s instead of 30s+.
#
# CRITICAL: opencode-ai npm package ships its Linux bundle as
# `lib/node_modules/opencode-ai/bin/opencode.exe` (yes, even on Linux —
# Bun-compiled single-file binary that npm names with .exe historically).
# npm itself creates the correct unsuffixed shim at
# `<nodebin>/opencode`. We must NOT hand-roll a symlink to the .exe file
# directly — we must point at npm's auto-generated shim, OR put the npm
# nvm bin dir on PATH and let `which opencode` resolve naturally.
# We do BOTH defensively so `opencode` resolves regardless of $PATH
# pruning by any caller / sandbox runtime.

FROM e2bdev/base:latest

# --- Base tools (curl + jq for opencode.json prep) ---
RUN apt-get update -y \
 && apt-get install -y --no-install-recommends curl ca-certificates jq \
 && rm -rf /var/lib/apt/lists/*

# --- Node 22 LTS via nvm ---
ENV NVM_DIR=/usr/local/nvm
RUN mkdir -p "$NVM_DIR" \
 && curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash \
 && . "$NVM_DIR/nvm.sh" \
 && nvm install 22 \
 && nvm alias default 22 \
 && NODE_BIN="$(dirname "$(nvm which 22)")" \
 && ln -sf "$NODE_BIN/node" /usr/local/bin/node \
 && ln -sf "$NODE_BIN/npm"  /usr/local/bin/npm \
 && ln -sf "$NODE_BIN/npx"  /usr/local/bin/npx \
 && echo "NODE_BIN=$NODE_BIN" > /etc/profile.d/node_bin.sh

# --- opencode CLI + provider adapters + registered MCP tools ---
#
# Pre-installed globally into the v22 node_modules so `npx -y <pkg>`
# at sandbox spawn finds them without a network round-trip (saves
# ~20s on cold starts).
#
# Registered MCP tools that get pre-warmed here MUST mirror the
# server-side tool registry in server/internal/tool/registry.go:
#   - github_mcp → @modelcontextprotocol/server-github
# When the registry adds a new tool, append the package here and
# rebuild + re-publish the template (semver-bump the tag, e.g.
# parsar-opencode-base:v0.3-<tool>).
RUN . "$NVM_DIR/nvm.sh" && nvm use default \
 && npm install -g \
      opencode-ai \
      @ai-sdk/anthropic \
      @ai-sdk/openai \
      @ai-sdk/google \
      @ai-sdk/openai-compatible \
      @modelcontextprotocol/server-github \
 && NODE_BIN="$(dirname "$(nvm which 22)")" \
 && ls -la "$NODE_BIN/opencode" \
 && ln -sf "$NODE_BIN/opencode" /usr/local/bin/opencode \
 && /usr/local/bin/opencode --version

# --- workspace + PATH including the nvm v22 bin dir ---
WORKDIR /home/user
# Hardcode nvm v22 bin path into PATH so callers that override PATH still get
# opencode + node + npm. Use a glob expansion at runtime via /etc/profile.d
# would not help non-login shells; baking the v22.x path in is acceptable
# because `nvm install 22` pins to the current latest 22.x — if 22.x bumps
# we rebuild the template anyway.
ENV PATH=/usr/local/bin:/usr/local/nvm/versions/node/v22.22.3/bin:${PATH}
