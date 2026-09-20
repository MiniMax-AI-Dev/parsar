#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output_dir="${AGENTS_API_BUILD_DIR:-$runtime_root/build/agents-api}"
for directory in "$runtime_root" "$output_dir"; do
  if [[ "$directory" != /* ]]; then
    printf 'Agents API build directories must be absolute: %s\n' "$directory" >&2
    exit 1
  fi
done

mkdir -p "$runtime_root/cache/agents-api-builds"
build_context="$(mktemp -d "$runtime_root/cache/agents-api-builds/source.XXXXXX")"
trap 'rm -rf "$build_context"' EXIT

# This is the release source boundary. Product and other application sources
# must remain physically absent, even when building from the full monorepo.
tar -C "$repo_root" -cf - \
  go.mod go.sum \
  contracts/agents-api/v1 \
  internal/agentdaemon/device internal/agentdaemon/gateway internal/agentdaemon/proto \
  internal/agentskill internal/obs/log services/agents-api \
  | tar -C "$build_context" -xf -

(
  cd "$build_context"
  export GOWORK=off CGO_ENABLED=0
  for command in server migrate device environment-key; do
    artifact="agents-api-$command"
    if [[ "$command" == server ]]; then artifact=agents-api; fi
    go build -mod=readonly -trimpath -buildvcs=false \
      -o "$build_context/bin/$artifact" "./services/agents-api/cmd/$command"
  done
)

# Publish only after every command builds successfully.
mkdir -p "$output_dir"
for artifact in agents-api agents-api-migrate agents-api-device agents-api-environment-key; do
  mv -f "$build_context/bin/$artifact" "$output_dir/$artifact"
done
printf 'Standalone Agents API binaries: %s\n' "$output_dir"
