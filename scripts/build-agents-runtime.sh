#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output_dir="${AGENTS_RUNTIME_BUILD_DIR:-$runtime_root/build/agents-runtime}"
# Extract the official @openai/codex@0.153.4-linux-x64 npm package here.
package_dir="${AGENTS_RUNTIME_CODEX_PACKAGE:?Set AGENTS_RUNTIME_CODEX_PACKAGE to the extracted pinned platform package}"
helpers_dir="${AGENTS_EXECUTOR_BUILD_DIR:-$runtime_root/build/agents-executor}"
for directory in "$runtime_root" "$output_dir" "$package_dir" "$helpers_dir"; do
  if [[ "$directory" != /* ]]; then
    printf 'Runtime build directories must be absolute: %s\n' "$directory" >&2
    exit 1
  fi
done
python3 - "$package_dir/package.json" <<'PY'
import json, sys
package = json.load(open(sys.argv[1]))
assert package['name'] == '@openai/codex' and package['version'] == '0.153.4-linux-x64', 'Expected pinned official Linux x64 package'
PY
native_dir="$package_dir/vendor/x86_64-unknown-linux-musl"
for executable in "$native_dir/bin/codex" "$helpers_dir/agents-api-codex-directory" "$helpers_dir/agents-api-codex-write" "$helpers_dir/agents-api-workspace-export"; do
  test -x "$executable" || { printf 'Missing executable: %s\n' "$executable" >&2; exit 1; }
done
test -d "$native_dir/codex-resources"
mkdir -p "$runtime_root/cache/agents-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/agents-runtime-builds/bundle.XXXXXX")"
trap 'rm -rf "$context"' EXIT
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath \
    -o "$context/parsar-daemon" ./apps/parsar-daemon/cmd/parsar-daemon
)
cp "$helpers_dir/agents-api-codex-directory" "$helpers_dir/agents-api-codex-write" "$helpers_dir/agents-api-workspace-export" "$context/"
cp "$native_dir/bin/codex" "$context/codex"
cp -R "$native_dir/codex-resources" "$context/codex-resources"
cp "$repo_root/services/agents-api/deploy/codex/tool-env.py" "$context/tool-env.py"
cp "$repo_root/services/agents-api/deploy/codex/requirements.toml" "$context/requirements.toml"
cp "$repo_root/services/agents-api/deploy/codex/Dockerfile" "$context/Dockerfile"
cp "$repo_root/services/agents-api/deploy/runtime/initialize.py" "$context/runtime-initialize.py"
# Preserve the previous bundle if compilation or validation failed.
mkdir -p "$output_dir"
cp -R "$context/." "$output_dir/"
printf 'Runtime image context: %s\n' "$output_dir"
printf 'Build with: docker build --platform linux/amd64 -t agents-runtime:local %q\n' "$output_dir"
