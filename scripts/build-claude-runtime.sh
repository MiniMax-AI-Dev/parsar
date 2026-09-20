#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output_dir="${AGENTS_RUNTIME_BUILD_DIR:-$runtime_root/build/claude-runtime}"
sdk_dir="${CLAUDE_SDK_BUILD_DIR:-$runtime_root/build/claude-sdk-runtime}"
helpers_dir="${AGENTS_EXECUTOR_BUILD_DIR:-$runtime_root/build/agents-executor}"
for directory in "$runtime_root" "$output_dir" "$sdk_dir" "$helpers_dir"; do
  [[ "$directory" == /* ]] || { printf 'Expected absolute build directory: %s\n' "$directory" >&2; exit 1; }
done
mkdir -p "$runtime_root/cache/agents-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/agents-runtime-builds/claude.XXXXXX")"
trap 'rm -rf "$context"' EXIT
archive=claude-sdk-runtime-linux-x64-glibc.tar.gz
(cd "$sdk_dir" && sha256sum -c "$archive.sha256")
mkdir "$context/claude-sdk"
tar -xzf "$sdk_dir/$archive" -C "$context/claude-sdk"
node "$repo_root/scripts/check-claude-sdk-runtime.mjs" "$context/claude-sdk"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath \
    -o "$context/parsar-daemon" ./apps/parsar-daemon/cmd/parsar-daemon
)
for helper in agents-api-codex-directory agents-api-codex-write agents-api-workspace-export; do
  test -x "$helpers_dir/$helper"
  cp "$helpers_dir/$helper" "$context/"
done
cp "$repo_root/services/agents-api/deploy/claude/Dockerfile" "$context/Dockerfile"
cp "$repo_root/services/agents-api/deploy/runtime/initialize.py" "$context/runtime-initialize.py"
cp "$repo_root/services/agents-api/deploy/runtime/build-system-seed.py" "$repo_root/services/agents-api/deploy/runtime/tool-root.py" "$context/"
mkdir -p "$output_dir"
cp -R "$context/." "$output_dir/"
printf 'Claude Runtime image context: %s\n' "$output_dir"
