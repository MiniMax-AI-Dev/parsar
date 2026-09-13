#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output_dir="${AGENTS_EXECUTOR_BUILD_DIR:-$runtime_root/build/agents-executor}"
for directory in "$runtime_root" "$output_dir"; do
  if [[ "$directory" != /* ]]; then
    printf 'Executor build directories must be absolute\n' >&2
    exit 1
  fi
done
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  printf 'The executor build currently supports Linux x86_64\n' >&2
  exit 1
fi

export CARGO_HOME="${CARGO_HOME:-$runtime_root/cache/executor-cargo}"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$runtime_root/cache/executor-target}"
for directory in "$CARGO_HOME" "$CARGO_TARGET_DIR"; do
  if [[ "$directory" != /* ]]; then
    printf 'Executor Cargo directories must be absolute\n' >&2
    exit 1
  fi
done
mkdir -p "$runtime_root/cache/executor-builds"
build_context="$(mktemp -d "$runtime_root/cache/executor-builds/source.XXXXXX")"
trap 'rm -rf "$build_context"' EXIT
for file in Cargo.toml Cargo.lock rust-toolchain.toml; do
  cp "$repo_root/packages/codex-executor/$file" "$build_context/$file"
done
cp -R "$repo_root/packages/codex-executor/src" "$build_context/src"
(
  cd "$build_context"
  cargo build --locked --release
)
mkdir -p "$output_dir"
cp "$CARGO_TARGET_DIR/release/agents-api-codex-executor" "$output_dir/agents-api-codex-executor.tmp"
mv -f "$output_dir/agents-api-codex-executor.tmp" "$output_dir/agents-api-codex-executor"
printf 'Standalone Codex executor: %s\n' "$output_dir/agents-api-codex-executor"
