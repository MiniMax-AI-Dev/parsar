SHELL := /bin/bash

# Production image knobs. Override at the CLI:
#   make docker-build PARSAR_IMAGE=parsar PARSAR_IMAGE_TAG=v0.1.0
# PARSAR_IMAGE is intentionally a generic local name — the open-source
# project does not own a default registry. Operators retag with
# `docker tag` / `docker push` after the build.
PARSAR_IMAGE     ?= parsar
PARSAR_IMAGE_TAG ?= dev

.PHONY: setup dev check reset-dev clean-dev paths seed-dev seed-dev-db migrate-dev sqlc-generate server web cli devgateway http-runner-once http-runner-loop dev-all smoke e2e-http-agent e2e-feishu-gateway dev-server-up dev-server-down dev-server-log bootstrap docker-build docker-build-no-cache docker-image-info openapi

setup:
	./scripts/setup.sh

paths:
	./scripts/parsar-paths.sh

seed-dev:
	pnpm --filter @parsar/cli parsar -- seed-dev

seed-dev-db:
	cd server && go run ./cmd/seeddev

migrate-dev:
	cd server && go run ./cmd/migrate

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
	cd server && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

dev:
	./scripts/dev-stack.sh

check:
	./scripts/check.sh

reset-dev:
	./scripts/reset-dev.sh

clean-dev:
	./scripts/reset-dev.sh --all

server:
	cd server && go run ./cmd/server

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
	pnpm --filter @parsar/cli parsar -- --help

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

# Quick sanity print so operators can confirm what they just built
# without piecing together `docker images | grep ...`. The size
# column is the strongest "I shipped the right thing" signal — if
# the runtime image is >300 MB the multi-stage build broke and
# someone is shipping the Go toolchain.
docker-image-info:
	@docker image inspect \
	    --format 'image={{.RepoTags}} id={{.Id}} created={{.Created}} size={{.Size}}' \
	    $(PARSAR_IMAGE):$(PARSAR_IMAGE_TAG)

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

openapi:
	@command -v swag >/dev/null 2>&1 || \
	    go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)
	@mkdir -p docs/openapi
	swag init \
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
