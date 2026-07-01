#!/usr/bin/env bash
#
# Build the local docker sandbox image (parsar-sandbox:local) for the
# docker-backed SandboxProvider (AGENT_DAEMON_SANDBOX_BACKEND=docker).
#
# Compiles the parsar + parsar-daemon CLIs as static linux/arm64 binaries
# (the container runs linux/aarch64 under Docker Desktop on Apple Silicon),
# stages them alongside the shared claude/opencode hooks, then builds the
# image from local.Dockerfile.
#
# Usage: infra/e2b-templates/parsar-sandbox-local/build.sh
# Override the tag: PARSAR_SANDBOX_LOCAL_IMAGE=foo:bar build.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
BUILD_DIR="${SCRIPT_DIR}/.build"
HOOKS_SRC="${SCRIPT_DIR}/../parsar-daemon-claudecode/hooks"
IMAGE="${PARSAR_SANDBOX_LOCAL_IMAGE:-parsar-sandbox:local}"
TARGET_ARCH="${PARSAR_SANDBOX_LOCAL_ARCH:-arm64}"

echo "[build] repo root: ${REPO_ROOT}"
echo "[build] image tag: ${IMAGE} (linux/${TARGET_ARCH})"

rm -rf "${BUILD_DIR}"
mkdir -p "${BUILD_DIR}"

echo "[build] compiling parsar-daemon + parsar (static linux/${TARGET_ARCH})"
( cd "${REPO_ROOT}" && CGO_ENABLED=0 GOOS=linux GOARCH="${TARGET_ARCH}" GOFLAGS=-trimpath \
    go build -ldflags='-s -w' -o "${BUILD_DIR}/parsar-daemon" ./apps/parsar-daemon/cmd/parsar-daemon )
( cd "${REPO_ROOT}" && CGO_ENABLED=0 GOOS=linux GOARCH="${TARGET_ARCH}" GOFLAGS=-trimpath \
    go build -ldflags='-s -w' -o "${BUILD_DIR}/parsar" ./apps/parsar/cmd/parsar )

echo "[build] staging hooks from ${HOOKS_SRC}"
cp -R "${HOOKS_SRC}" "${BUILD_DIR}/hooks"

echo "[build] docker build"
docker build --platform "linux/${TARGET_ARCH}" \
  -f "${SCRIPT_DIR}/local.Dockerfile" \
  -t "${IMAGE}" \
  "${BUILD_DIR}"

echo "[build] done: ${IMAGE}"
echo "[build] run the server with:"
echo "        AGENT_DAEMON_SANDBOX_BACKEND=docker AGENT_DAEMON_SANDBOX_DOCKER_IMAGE=${IMAGE} <server>"
