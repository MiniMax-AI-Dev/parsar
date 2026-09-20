#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output="${AGENTS_RUNTIME_BUILD_DIR:-$runtime_root/build/mcode-runtime}"
native="${MCODE_CLI_DIR:?Set MCODE_CLI_DIR to the installed published package directory}"
companion="${MCODE_HARNESS_BUILD_DIR:?Set MCODE_HARNESS_BUILD_DIR to the built companion}"
helpers="${AGENTS_EXECUTOR_BUILD_DIR:-$runtime_root/build/agents-executor}"
for directory in "$runtime_root" "$output" "$native" "$companion" "$helpers"; do
  [[ "$directory" == /* ]] || { printf 'Absolute build directories are required\n' >&2; exit 1; }
done
test -f "$companion/provenance.json"
test "$(node "$native/cli.js" --version)" = 0.4.12
mkdir -p "$runtime_root/cache/agents-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/agents-runtime-builds/mcode.XXXXXX")"
trap 'rm -rf "$context"' EXIT
mkdir "$context/mcode" "$context/mcode-harness"
cp -RL "$native/." "$context/mcode/"
cp -R "$companion/." "$context/mcode-harness/"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath \
    -o "$context/parsar-daemon" ./apps/parsar-daemon/cmd/parsar-daemon
)
for helper in agents-api-codex-directory agents-api-codex-write agents-api-workspace-export; do
  test -x "$helpers/$helper"
  cp "$helpers/$helper" "$context/"
done
cp "$repo_root/services/agents-api/deploy/mcode/Dockerfile" "$context/Dockerfile"
cp "$repo_root/services/agents-api/deploy/runtime/initialize.py" "$context/runtime-initialize.py"
cp "$repo_root/services/agents-api/deploy/runtime/build-system-seed.py" "$repo_root/services/agents-api/deploy/runtime/tool-root.py" "$context/"
mkdir -p "$output"
cp -R "$context/." "$output/"
printf 'MiniMax Code Runtime image context: %s\n' "$output"
