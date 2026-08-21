SHELL := /bin/bash

GO_TEST_PACKAGE ?= $(shell cd server && go list ./... | grep -Ev 'internal/(store|seed)$$')
GO_TEST_RUN ?=
GO_TEST_ARGS ?=
SQLC_VERSION ?= v1.29.0
SQLC ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

ifneq ($(strip $(GO_TEST_RUN)),)
GO_TEST_RUN_FLAG := -run '$(GO_TEST_RUN)'
endif

# Production image knobs. Override at the CLI:
#   make docker-build PARSAR_IMAGE=parsar PARSAR_IMAGE_TAG=v0.1.0
# PARSAR_IMAGE is intentionally a generic local name — the open-source
# project does not own a default registry. Operators retag with
# `docker tag` / `docker push` after the build.
PARSAR_IMAGE     ?= parsar
PARSAR_IMAGE_TAG ?= dev

.PHONY: help setup node-deps dev dev-db check check-setup check-sqlc check-go check-store check-web check-cli check-hygiene test test-fast test-go test-web typecheck-web lint-web-design lint-web test-cli typecheck reset-dev clean-dev paths migrate-dev sqlc-generate server web cli devgateway http-runner-once http-runner-loop dev-all smoke e2e-http-agent e2e-feishu-gateway dev-server-up dev-server-down dev-server-log bootstrap docker-build docker-build-no-cache openapi e2b-template e2b-template-binaries

help:
	@printf '%s\n' \
	  'Local development:' \
	  '  make dev-all          Start Postgres, API, web, and HTTP runner' \
	  '  make dev-db           Start the development Postgres only' \
	  '  make server           Run the API in the foreground' \
	  '  make web              Run the web app in the foreground' \
	  '' \
	  'Fast feedback:' \
	  '  make test-fast        Run Go tests plus frontend/CLI checks' \
	  '  make test-go          Run all Go tests without integration DB packages' \
	  '  make test-go GO_TEST_PACKAGE=./server/internal/api/...' \
	  '  make test-go GO_TEST_PACKAGE=./server/internal/api GO_TEST_RUN=TestHealth' \
	  '  make test-web         Typecheck web and check design-system lint' \
	  '  make lint-web         Run the full web lint (existing debt may fail)' \
	  '  make test-cli         Run CLI unit tests' \
	  '  make typecheck        Typecheck all TypeScript packages' \
	  '' \
	  'Before review:' \
	  '  make check            Run the required full repository gate' \
	  '  make check-go         Run sqlc drift check and non-store Go tests' \
	  '  make check-store      Run migration and store integration tests' \
	  '  make check-web        Run web typecheck and design lint' \
	  '  make check-cli        Typecheck CLI/plugin packages' \
	  '' \
	  'Cloud sandbox (e2b):' \
	  '  make e2b-template     Cross-compile binaries and build the e2b sandbox template' \
	  '  make e2b-template E2B_TEMPLATE_NAME=my-sandbox'

setup:
	./scripts/setup.sh

node-deps:
	@if [[ ! -d node_modules ]]; then pnpm install --frozen-lockfile; fi

paths:
	./scripts/setup.sh paths

migrate-dev:
	./scripts/with-dev-env.sh bash -c 'cd server && exec go run ./cmd/migrate'

# `make bootstrap` is the operator-side first-owner provisioning
# entry point for a freshly-installed Parsar. Required flags must
# be passed via env so the CLI honours the --email / --workspace /
# --name arguments. Example:
#
#   DATABASE_URL=postgres://... \
#   PARSAR_BOOTSTRAP_EMAIL=admin@example.com \
#   PARSAR_BOOTSTRAP_WORKSPACE="Acme Corp" \
#   PARSAR_BOOTSTRAP_NAME="First Admin" \
#   make bootstrap
bootstrap:
	cd server && go run ./cmd/parsar-bootstrap \
		--email=$${PARSAR_BOOTSTRAP_EMAIL:?PARSAR_BOOTSTRAP_EMAIL is required} \
		--workspace=$${PARSAR_BOOTSTRAP_WORKSPACE:?PARSAR_BOOTSTRAP_WORKSPACE is required} \
		--name=$${PARSAR_BOOTSTRAP_NAME:-}

sqlc-generate:
	cd server && $(SQLC) generate

dev-db:
	./scripts/dev-stack.sh

# Backward-compatible alias. Prefer `make dev-db` for the DB-only dev stack.
dev: dev-db

check: check-go check-store check-web check-cli check-hygiene
	@printf 'Parsar harness checks passed.\n'

check-setup:
	./scripts/setup.sh >/dev/null

check-sqlc:
	@set -e; \
	before_sqlc_status="$$(cd server && git status --short -- internal/db/sqlc)"; \
	(cd server && $(SQLC) generate); \
	after_sqlc_status="$$(cd server && git status --short -- internal/db/sqlc)"; \
	if [[ "$$before_sqlc_status" != "$$after_sqlc_status" ]]; then \
	  echo "sqlc generated files are out of date" >&2; \
	  printf '%s\n' "$$after_sqlc_status" >&2; \
	  exit 1; \
	fi

check-go: check-setup check-sqlc test-go

check-store: check-setup
	./scripts/check-migrations.sh

check-web: check-setup typecheck-web lint-web-design

check-cli: check-setup node-deps
	pnpm --filter @parsar/cli typecheck
	pnpm --filter @parsar/opencode-plugin typecheck

check-hygiene: check-setup
	@for polluted in .parsar logs state cache config; do \
	  if [[ -e "$$polluted" ]]; then \
	    echo "CWD pollution detected: $$polluted" >&2; \
	    exit 1; \
	  fi; \
	done
	@if grep -R "[>/]tmp/parsar" scripts server --exclude='check.sh' >/dev/null 2>&1; then \
	  echo "Runtime logs/state must live under ~/.parsar, not /tmp/parsar*" >&2; \
	  exit 1; \
	fi

# Fast local feedback. Unlike `make check`, these targets skip setup,
# code-generation drift checks, and database-backed migration tests.
test: test-fast

test-fast: test-go test-web test-cli

test-go:
	go test $(GO_TEST_PACKAGE) $(GO_TEST_RUN_FLAG) $(GO_TEST_ARGS)

test-web: typecheck-web lint-web-design

typecheck-web: node-deps
	pnpm --filter @parsar/web typecheck

lint-web-design: node-deps
	@if (cd apps/web && npx eslint src/ 2>&1) | grep -q "no-restricted-syntax"; then \
	  echo "Design-system lint violations (arbitrary font sizes or raw palette):" >&2; \
	  (cd apps/web && npx eslint src/ 2>&1) | grep "no-restricted-syntax" >&2; \
	  exit 1; \
	fi

lint-web: node-deps
	pnpm --filter @parsar/web lint

test-cli: node-deps
	pnpm --filter @parsar/cli test

typecheck: node-deps
	pnpm typecheck

reset-dev:
	./scripts/reset-dev.sh

clean-dev:
	./scripts/reset-dev.sh --all

server:
	./scripts/with-dev-env.sh bash -c 'cd server && exec go run ./cmd/server'

# Persistent dev server lifecycle. The binary lives at ~/.parsar/bin
# and runs inside a tmux session that survives sandbox bash exits and
# launchctl plist evictions. See scripts/dev-server-up.sh for the
# full rationale.
dev-server-up:
	./scripts/dev-server-up.sh

dev-server-down:
	./scripts/dev-server-down.sh

dev-server-log:
	./scripts/dev-server-log.sh

web:
	pnpm --filter @parsar/web dev

cli:
	pnpm --filter @parsar/cli parsar --help

devgateway:
	cd server && go run ./cmd/devgateway --help

http-runner-once:
	cd server && go run ./cmd/httprunner --once

http-runner-loop:
	cd server && go run ./cmd/httprunner --interval $${PARSAR_HTTP_RUNNER_INTERVAL:-2s} --max-runs $${PARSAR_HTTP_RUNNER_MAX_RUNS:-100}

dev-all:
	./scripts/dev-all.sh

# Self-hosted smoke test. Pass PARSAR_API_URL to point at a
# deployed server (default http://127.0.0.1:8080). Runs against a real
# deployed server — no dev seed data or fake IM mocks required.
smoke:
	./scripts/smoke.sh

e2e-http-agent:
	./scripts/e2e-http-agent.sh

e2e-feishu-gateway:
	./scripts/e2e-feishu-gateway.sh

# --- Production image (self-hosted deployment artifact) ----------------
#
# `make docker-build` produces a self-contained image carrying server +
# migrate + bootstrap binaries, the Vite SPA, and the SQL migrations.
# The build writes ONLY into Docker's own layer cache — nothing under
# the repo working directory is touched, so the CWD stays clean (per
# AGENTS.md hard rule).
docker-build:
	docker build \
	    -t $(PARSAR_IMAGE):$(PARSAR_IMAGE_TAG) \
	    -f Dockerfile \
	    .

# Bypass the layer cache when you need to confirm a fresh resolve
# of pnpm-lock.yaml / Go module proxy. Slower; use it before tagging
# a release candidate.
docker-build-no-cache:
	docker build \
	    --no-cache \
	    --pull \
	    -t $(PARSAR_IMAGE):$(PARSAR_IMAGE_TAG) \
	    -f Dockerfile \
	    .

# --- OpenAPI spec (generated from swaggo annotations) ------------------
#
# Runs `swag init` over server/**/*.go. Every handler carrying a
#   //  @Router  /path  [verb]
# annotation block contributes an operation; the general @title/@version
# come from the swag block above `package main` in cmd/server/main.go.
#
# The spec is a build artifact — do NOT edit docs/openapi/openapi.yaml
# by hand. The CI check regenerates it and fails the build on any diff
# from the committed version, so drift is always caught at PR time.
#
# Requires the swag CLI:
#     go install github.com/swaggo/swag/cmd/swag@v1.16.4
# The recipe auto-installs on first use.
SWAG_VERSION ?= v1.16.4
SWAG ?= $(shell command -v swag 2>/dev/null || printf '%s/bin/swag' "$$(go env GOPATH)")

openapi:
	@command -v $(SWAG) >/dev/null 2>&1 || \
	    go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)
	@mkdir -p docs/openapi
	$(SWAG) init \
	    -g server/cmd/server/main.go \
	    --dir . \
	    --output docs/openapi/gen \
	    --outputTypes yaml \
	    --parseInternal \
	    --parseDepth 100 \
	    --exclude ./apps,./packages,./node_modules,./tests,./infra
	@mv docs/openapi/gen/swagger.yaml docs/openapi/openapi.yaml
	@rmdir docs/openapi/gen 2>/dev/null || true
	@echo "openapi: wrote docs/openapi/openapi.yaml"
	@echo "openapi: paths=$$(grep -c '^  /' docs/openapi/openapi.yaml)"

# --- E2B sandbox template ----------------------------------------------
#
# Builds the cloud-isolation sandbox template on e2b.app. Collapses the
# manual sequence (cross-compile two Go binaries, then invoke the e2b
# CLI from the right directory) into one command.
#
# Why the binaries are cross-compiled here rather than in the image:
# e2b's builder rejects multi-stage Dockerfiles, so infra/sandbox/
# e2b.Dockerfile cannot compile Go itself and instead COPYs prebuilt
# binaries out of infra/sandbox/.build/ (gitignored). linux/amd64 is
# hardcoded because e2b templates are amd64-only.
#
# The build context is infra/sandbox/ (not the repo root) so the upload
# stays small; every COPY source in e2b.Dockerfile lives under it.
#
# Requires the e2b CLI and an API key:
#     npm install -g @e2b/cli
#     export E2B_API_KEY=e2b_...
# PARSAR_E2B_API_KEY is accepted as a fallback so the same value already
# in .env (used by the server) works without being re-exported, and .env
# is sourced when present.
#
# After a successful build, point the server at the template:
#     AGENT_DAEMON_SANDBOX_TEMPLATE=<template id>
# Rebuilding an existing template name keeps the same id, so the id only
# has to be copied into .env on first creation.
E2B_TEMPLATE_NAME ?= parsar-sandbox
E2B_TEMPLATE_MEMORY_MB ?= 4096
E2B_BUILD_DIR := infra/sandbox/.build

e2b-template: e2b-template-binaries
	@set -euo pipefail; \
	if [[ -f .env ]]; then set -a; source .env; set +a; fi; \
	key="$${E2B_API_KEY:-$${PARSAR_E2B_API_KEY:-}}"; \
	if [[ -z "$$key" ]]; then \
	  echo "e2b-template: E2B_API_KEY (or PARSAR_E2B_API_KEY) is required" >&2; \
	  echo "              get one at https://e2b.dev/dashboard" >&2; \
	  exit 1; \
	fi; \
	command -v e2b >/dev/null 2>&1 || { \
	  echo "e2b-template: e2b CLI not found; install with 'npm install -g @e2b/cli'" >&2; \
	  exit 1; \
	}; \
	cd infra/sandbox && E2B_API_KEY="$$key" e2b template create $(E2B_TEMPLATE_NAME) \
	    --dockerfile e2b.Dockerfile \
	    --memory-mb $(E2B_TEMPLATE_MEMORY_MB)

# Cross-compile the two binaries the template image needs. Split out so
# it can be run on its own when iterating on daemon code.
e2b-template-binaries:
	@mkdir -p $(E2B_BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
	    -o $(E2B_BUILD_DIR)/parsar-daemon ./apps/parsar-daemon/cmd/parsar-daemon
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
	    -o $(E2B_BUILD_DIR)/parsar ./apps/parsar/cmd/parsar
	@echo "e2b-template: staged linux/amd64 binaries in $(E2B_BUILD_DIR)"
