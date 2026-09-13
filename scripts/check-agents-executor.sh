#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
if [[ "$runtime_root" != /* ]]; then
  printf 'Executor runtime directory must be absolute\n' >&2
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
cd "$repo_root/packages/codex-executor"
cargo fmt --all -- --check
cargo test --locked
cargo clippy --locked --all-targets -- -D warnings
