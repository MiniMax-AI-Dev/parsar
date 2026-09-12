#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
image="${AGENTS_API_IMAGE:-agents-api:dev}"
if [[ "$runtime_root" != /* ]]; then
  printf 'PARSAR_HOME must be absolute: %s\n' "$runtime_root" >&2
  exit 1
fi
mkdir -p "$runtime_root/cache/agents-api-builds"
image_context="$(mktemp -d "$runtime_root/cache/agents-api-builds/image.XXXXXX")"
trap 'rm -rf "$image_context"' EXIT

# Reuse the source boundary; never send the repository or runtime keys to Docker.
GOOS=linux GOARCH=amd64 AGENTS_API_BUILD_DIR="$image_context" \
  "$repo_root/scripts/build-agents-api.sh"
cp "$repo_root/services/agents-api/Dockerfile" "$image_context/Dockerfile"
docker build --platform linux/amd64 --tag "$image" "$image_context"
