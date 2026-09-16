#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package="$repo_root/packages/codex-harness"
runtime_root="$HOME/.parsar"
output_dir="${AGENTS_HARNESS_BUILD_DIR:-$runtime_root/build/agents-harness}"
native_source="${AGENTS_HARNESS_NATIVE_SOURCE:?Set AGENTS_HARNESS_NATIVE_SOURCE to the pinned upstream Git checkout}"
mode="${1:-build}"
if [[ "$mode" != build && "$mode" != check ]]; then
  printf 'Expected build or check mode\n' >&2
  exit 1
fi
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  printf 'The private harness supports Linux x86_64\n' >&2
  exit 1
fi
export CARGO_HOME="${CARGO_HOME:-$runtime_root/cache/agents-harness-cargo}"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$runtime_root/cache/agents-harness-target}"
export TMPDIR="$runtime_root/cache/agents-harness-tmp"
export RUSTUP_TOOLCHAIN="${RUSTUP_TOOLCHAIN:-1.95.0}"
rustc_version="$(rustc --version)"
if [[ "$rustc_version" != 'rustc 1.95.0 '* ]]; then
  printf 'The private harness requires rustc 1.95.0\n' >&2
  exit 1
fi
python3 "$package/prepare.py" --check --check-path "$output_dir" \
  --check-path "$CARGO_HOME" --check-path "$CARGO_TARGET_DIR" \
  --check-path "$TMPDIR" --check-path "$runtime_root/cache/agents-harness-builds"
mkdir -p "$runtime_root/cache/agents-harness-builds" "$TMPDIR"
build_context="$(mktemp -d "$runtime_root/cache/agents-harness-builds/source.XXXXXX")"
trap 'rm -rf "$build_context"' EXIT
python3 "$package/prepare.py" --source "$native_source" --output "$build_context/upstream"
cd "$build_context/upstream/codex-rs"
if [[ "$mode" == check ]]; then
  rustfmt --check --edition 2024 app-server/parsar-harness/*.rs
  cargo test --locked -p codex-app-server --bin parsar-codex-harness
  cargo clippy --locked -p codex-app-server --bin parsar-codex-harness -- -D warnings
  exit 0
fi
cargo build --locked --release -p codex-app-server --bin parsar-codex-harness
mkdir -p "$output_dir"
cp "$CARGO_TARGET_DIR/release/parsar-codex-harness" "$output_dir/parsar-codex-harness.tmp"
mv -f "$output_dir/parsar-codex-harness.tmp" "$output_dir/parsar-codex-harness"
python3 - "$build_context/upstream/preparation.json" "$output_dir" <<'PY'
import hashlib
import json
import os
import pathlib
import subprocess
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text())
output = pathlib.Path(sys.argv[2])
record["artifact_sha256"] = hashlib.sha256((output / "parsar-codex-harness").read_bytes()).hexdigest()
record["rustc"] = subprocess.check_output(["rustc", "--version"], text=True).strip()
record["cargo"] = subprocess.check_output(["cargo", "--version"], text=True).strip()
record["build_profile"] = "release"
record["profile_overrides"] = {key: value for key, value in os.environ.items() if key.startswith("CARGO_PROFILE_RELEASE_")}
pending = output / "provenance.json.tmp"
pending.write_text(json.dumps(record, indent=2) + "\n")
pending.replace(output / "provenance.json")
PY
printf 'Private Codex harness: %s\n' "$output_dir/parsar-codex-harness"
