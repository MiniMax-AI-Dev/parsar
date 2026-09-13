#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output_dir="${CLAUDE_SDK_BUILD_DIR:-$runtime_root/build/claude-sdk-runtime}"
for directory in "$runtime_root" "$output_dir"; do
  if [[ "$directory" != /* ]]; then
    printf 'Claude SDK build directories must be absolute: %s\n' "$directory" >&2
    exit 1
  fi
done

node -e 'if (Number(process.versions.node.split(".")[0]) < 20) throw new Error("Claude SDK runtime builds require Node 20 or newer")'
mkdir -p "$runtime_root/cache/claude-sdk-builds"
build_context="$(mktemp -d "$runtime_root/cache/claude-sdk-builds/runtime.XXXXXX")"
trap 'rm -rf "$build_context"' EXIT
cd "$repo_root"
# Validate source manifests before deploy derives its dedicated frozen lockfile.
test -f pnpm-lock.yaml
pnpm install --frozen-lockfile
pnpm --filter @parsar/claude-sdk-adapter build --outDir "$build_context/compiled"
# This package has registry dependencies only. Keep injection local to export;
# the ordinary workspace and product installs retain their current settings.
pnpm --config.inject-workspace-packages=true --config.extend-node-path=false \
  --filter @parsar/claude-sdk-adapter \
  deploy --prod "$build_context/runtime"
# Discard any incremental checkout output copied by the package exporter.
rm -rf "$build_context/runtime/dist"
mv "$build_context/compiled" "$build_context/runtime/dist"
node scripts/check-claude-sdk-runtime.mjs "$build_context/runtime"

platform="$(node -p 'process.platform + "-" + process.arch + (process.platform === "linux" ? (process.report.getReport().header.glibcVersionRuntime ? "-glibc" : "-musl") : "")')"
archive="claude-sdk-runtime-$platform.tar.gz"
tar -C "$build_context/runtime" -czf "$build_context/$archive" .
node - "$build_context/$archive" "$archive" > "$build_context/$archive.sha256" <<'JS'
const { createHash } = require("node:crypto");
const { readFileSync } = require("node:fs");
console.log(createHash("sha256").update(readFileSync(process.argv[2])).digest("hex") + "  " + process.argv[3]);
JS
mkdir -p "$output_dir"
mv -f "$build_context/$archive" "$build_context/$archive.sha256" "$output_dir/"
printf 'Claude SDK runtime: %s/%s\n' "$output_dir" "$archive"
