# syntax=docker/dockerfile:1.7
#
# Parsar production image.
#
# Builds a single image that contains:
#   - parsar-server     (default CMD, serves the API and the SPA)
#   - parsar-migrate    (goose migration runner)
#   - parsar-bootstrap  (first-owner provisioning CLI)
#   - parsar-daemon     (manual daemon runtime)
#   - /app/web/dist       (Vite build output, served via PARSAR_WEB_DIST)
#   - /app/migrations     (SQL migrations, picked up via PARSAR_MIGRATIONS_DIR)
#
# The image carries NO real secrets and NO internal addresses.
# Operators inject DATABASE_URL / PARSAR_MASTER_KEY / Feishu OIDC /
# E2B credentials / etc. at runtime via environment variables or a
# YAML config file mounted at $PARSAR_CONFIG_FILE.
#
# Build:        make docker-build
# Run server:   docker run --rm -p 8080:8080 \
#                 -e DATABASE_URL=postgres://... \
#                 -e PARSAR_MASTER_KEY=... \
#                 parsar:dev
# Run migrate:  docker run --rm \
#                 -e DATABASE_URL=postgres://... \
#                 parsar:dev parsar-migrate
# Run bootstrap: docker run --rm \
#                 -e DATABASE_URL=postgres://... \
#                 parsar:dev parsar-bootstrap \
#                   --email=admin@example.com --workspace=Acme

###############################################################################
# Stage 1: web-builder — build the Vite SPA.
#
# Workspace deps are copied first so `pnpm install` lands on a cached
# layer when source changes but dependencies don't. The full source
# is copied in a later layer that re-runs only on source edits.
###############################################################################
ARG NODE_VERSION=22-alpine
ARG GO_VERSION=1.25-bookworm
ARG RUNTIME_BASE=debian:bookworm-slim

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION} AS web-builder
ENV PNPM_HOME=/pnpm
ENV PATH=/pnpm:$PATH
RUN corepack enable && corepack prepare pnpm@10.30.3 --activate
WORKDIR /src

# Workspace + lockfile.
COPY pnpm-workspace.yaml package.json pnpm-lock.yaml ./
# Per-package manifests so pnpm can resolve the workspace graph
# without the source tree. Listing them explicitly keeps the install
# layer cache stable; adding/removing a workspace package requires
# updating this list AND the lockfile.
COPY apps/web/package.json apps/web/
COPY packages/cli/package.json packages/cli/
COPY packages/eslint-config/package.json packages/eslint-config/
COPY packages/opencode-plugin/package.json packages/opencode-plugin/

# Install only the deps the web app actually needs.
RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    pnpm install --store-dir /pnpm/store --filter @parsar/web... --frozen-lockfile

# Source for the web app (and any workspace packages it imports).
COPY apps/web ./apps/web
COPY packages ./packages

RUN pnpm --filter @parsar/web build

###############################################################################
# Stage 2: go-builder — compile the Go binaries.
#
# CGO is off so the binaries are statically linked and can run on a
# minimal runtime. trimpath strips build-host file paths from the
# binary (defence-in-depth against operator info leaks).
###############################################################################
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS go-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Module graph first, source second — keeps `go mod download` cacheable.
COPY go.mod go.sum go.work ./
# go.work.sum may not exist on freshly-cloned checkouts; the trailing
# glob makes the COPY tolerate that.
COPY go.work.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY internal ./internal
COPY server ./server

# Build server-side binaries in one RUN so the layer represents one
# logical "compile" operation. -s -w strips debug info / symbol table;
# combined they shave ~25% off binary size with no runtime cost.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd server \
 && mkdir -p /out \
 && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOFLAGS=-trimpath go build -ldflags="-s -w" \
      -o /out/parsar-server    ./cmd/server \
 && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOFLAGS=-trimpath go build -ldflags="-s -w" \
      -o /out/parsar-migrate   ./cmd/migrate \
 && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOFLAGS=-trimpath go build -ldflags="-s -w" \
      -o /out/parsar-bootstrap ./cmd/parsar-bootstrap

# parsar-daemon source (root module, no separate go.mod). Copied after the
# server build so editing the daemon doesn't invalidate the layer above.
COPY apps/parsar-daemon ./apps/parsar-daemon

# Cross-compile the daemon for every platform the install script serves —
# the allowlist in server/internal/api/parsar_daemon_download.go is
# {darwin,linux}×{amd64,arm64}. Baking all four into the image lets the
# minting server hand the host-appropriate binary to the one-line connect
# command with no GitHub release (PARSAR_DAEMON_CONNECT_URL path). CGO
# stays off so each cross-target links statically.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out/daemon \
 && for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do \
      os="${target%/*}"; arch="${target#*/}"; \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOFLAGS=-trimpath \
        go build -ldflags="-s -w" \
          -o "/out/daemon/parsar-daemon-${os}-${arch}" \
          ./apps/parsar-daemon/cmd/parsar-daemon; \
    done

RUN cp "/out/daemon/parsar-daemon-linux-${TARGETARCH}" /out/parsar-daemon

###############################################################################
# Stage 3: runtime — debian-slim with a non-root user.
#
# debian-slim (not distroless / scratch) on purpose:
#   - ca-certificates + tini ship pre-built.
#   - Operators can `docker exec -it ... bash` to debug.
#   - The opencode local runner may shell out (rg, basic core utils);
#     keeping a real userland avoids surprises.
###############################################################################
FROM --platform=$TARGETPLATFORM ${RUNTIME_BASE} AS runtime

ARG PARSAR_USER=parsar
ARG PARSAR_UID=10001
ARG PARSAR_GID=10001

# Single RUN to limit the resulting layer count. The apt operations
# touch the package index and would inflate the image if split.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        tini \
        wget \
    ; \
    rm -rf /var/lib/apt/lists/*; \
    groupadd --system --gid ${PARSAR_GID} ${PARSAR_USER}; \
    useradd  --system --uid ${PARSAR_UID} --gid ${PARSAR_GID} \
             --home-dir /var/lib/parsar \
             --shell /usr/sbin/nologin \
             ${PARSAR_USER}; \
    mkdir -p /var/lib/parsar /app/web /app/migrations; \
    chown -R ${PARSAR_UID}:${PARSAR_GID} /var/lib/parsar

# Binaries on $PATH — operators override CMD to switch between them.
COPY --from=go-builder /out/parsar-server    /usr/local/bin/parsar-server
COPY --from=go-builder /out/parsar-migrate   /usr/local/bin/parsar-migrate
COPY --from=go-builder /out/parsar-bootstrap /usr/local/bin/parsar-bootstrap
COPY --from=go-builder /out/parsar-daemon    /usr/local/bin/parsar-daemon

# Per-platform parsar-daemon binaries. RegisterParsarDaemonDownloadRoute
# serves these (from PARSAR_DAEMON_BINARY_DIR) to the one-line connect
# command, so a local or air-gapped self-host install needs no GitHub
# release. World-readable on purpose: the binary is public; the pairing
# token gates connecting. NOT on $PATH — they are other-OS/arch artefacts,
# not meant to run in this container.
COPY --from=go-builder /out/daemon /usr/local/share/parsar/daemon

# SPA artefacts. Owner = root, perms = world-readable (handler runs
# as the non-root parsar user and only needs read access).
COPY --from=web-builder /src/apps/web/dist /app/web/dist

# Migrations. Same ownership rationale as the SPA — read-only from
# the parsar user's perspective.
COPY server/migrations /app/migrations

# OpenAPI spec. Baked in so /docs (Swagger UI) and /api/v1/openapi.yaml
# work out of the box; RegisterDocsRoutes unmounts silently when the
# file is missing, which is what caused the SPA fallback previously.
COPY docs/openapi/openapi.yaml /app/docs/openapi/openapi.yaml

# Default deployment knobs. Override at `docker run -e ...` time;
# values are non-secret defaults that match the on-disk layout we
# just wrote above.
ENV PARSAR_ADDR=":8080" \
    PARSAR_DATA_DIR="/var/lib/parsar" \
    PARSAR_MIGRATIONS_DIR="/app/migrations" \
    PARSAR_WEB_DIST="/app/web/dist" \
    PARSAR_OPENAPI_SPEC="/app/docs/openapi/openapi.yaml" \
    PARSAR_DAEMON_BINARY_DIR="/usr/local/share/parsar/daemon"

USER ${PARSAR_USER}
WORKDIR /var/lib/parsar
VOLUME ["/var/lib/parsar"]
EXPOSE 8080

# Liveness-only healthcheck. /healthz never touches the database so a
# struggling Postgres does NOT loop the container restart — that lives
# in /readyz which orchestrators consume separately. See
# docs/deploy/health-and-smoke.md for the full probe contract.
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
  CMD wget --quiet --spider http://127.0.0.1:8080/healthz || exit 1

# tini reaps zombie subprocesses spawned by the opencode local runner
# (or any future fork-exec path). Without it the server PID-1 would
# leak zombies on every prompt that shells out and an SRE poking the
# image weeks later would find a process table full of `<defunct>`
# entries with no obvious culprit.
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/parsar-server"]
