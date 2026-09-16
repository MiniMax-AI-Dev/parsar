# Raw app-server manager qualification

This private experiment exposes the `EnvironmentManager` created by the stock
Codex raw runner. Native requests and typed filesystem operations can therefore
use the same manager without adopting the in-process notification queue. It adds
one explicit embedding entrypoint; the ordinary entrypoint retains its behavior.
The patch is a local dependency experiment, not an available upstream API or a
production runtime selection.

`source.json` pins Codex 0.153.4, commit
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`, the patch hash, Rust 1.95.0 and
the build lock overlay. The upstream lock records 149 workspace packages as
`0.0.0` while its manifest uses `0.153.4`. Preparation changes only those version
entries and checks the exact original/result hashes; no dependency resolver or
third-party update is involved. Keep this qualification separate from the public
Agents API protocol pin and the existing production native installation.

## Prepare and run

Use Linux with Python 3.10+, Git, tar, Rust 1.95.0, rustfmt, Clippy, a C toolchain,
pkg-config and OpenSSL development headers. Supply an existing official Codex Git
checkout containing the exact commit and its matching native executable/resources.
The source export uses the named commit, ignoring any local checkout changes.

From the Parsar worktree, set `NATIVE_SOURCE` and `CODEX_BINARY` to absolute paths:

```sh
export RAW_MANAGER_ROOT="$HOME/.parsar/raw-manager-qualification"
python3 services/agents-api/tests/native/raw_manager/prepare.py \
  --source "$NATIVE_SOURCE" --output "$RAW_MANAGER_ROOT/source"
mkdir -p "$RAW_MANAGER_ROOT/state"
export TMPDIR="$RAW_MANAGER_ROOT/state"
export CARGO_HOME="$HOME/.parsar/cache/agents-native-cargo"
export CARGO_TARGET_DIR="$HOME/.parsar/cache/raw-manager-target"
export RUSTUP_TOOLCHAIN=1.95
export CARGO_PROFILE_DEV_DEBUG=0
rustc --version # The installed toolchain must report 1.95.0.
cd "$RAW_MANAGER_ROOT/source/codex-rs"
cargo build --locked -p codex-app-server --example parsar_raw_manager_probe
cargo test --locked -p codex-app-server --example parsar_raw_manager_probe
cargo clippy --locked -p codex-app-server --example parsar_raw_manager_probe -- -D warnings
rustfmt --check --edition 2024 app-server/examples/parsar_raw_manager_probe.rs
cargo test --locked -p codex-app-server --lib transport::tests
for mode in smoke receiver-dropped startup-failure late-startup-failure; do
  "$CARGO_TARGET_DIR/debug/examples/parsar_raw_manager_probe" \
    "$mode" "$CODEX_BINARY" "$RAW_MANAGER_ROOT/state"
done
```

Use a new output directory for each prepared source. Failed preparation leaves
its partial directory for inspection and never overwrites an existing tree.
Retain `preparation.json`, command outputs/exit codes, toolchain versions and the
compiled probe/helper SHA-256 values with the acceptance evidence. A mismatched
patch or upstream lock fails preparation; there is no fallback to another pin.
Native tests and build are opt-in, separate from the repository's `make check`.

## What this establishes

The deterministic fixture starts the actual raw stdio runner, performs its native
handshake and adds a fresh Environment through native RPC. Looking up that new ID
through the published handle and using its typed filesystem checks shared manager
identity; a same-disk read/write alone would not. Failure modes exercise a dropped
manager receiver and startup failure. Existing native transport tests retain the
stock backpressure/disconnect regression coverage.

The fixture resolves proof paths within the caller's `~/.parsar` before creating
state. Its isolated child HOME retains the original caller context for this check;
the path test rejects misleading components, parent traversal and symlink escapes.

Publication means that a manager handle exists. It does not establish completed
initialization, remote readiness, caller authorization or revocation. An `Arc` may
outlive the runner, so a future owner must supervise failure and release it.

There are no model calls in this seam qualification. Real remote Files plus model
execution/cancellation/cold-history composition remain required before adoption.
Idle ownership, tenant/generation checks, workspace confinement, unknown write
outcomes and public Files paths/references/pagination remain separate work. This
does not fix or validate every native event queue, and it does not change the
existing shared-manager probe's recorded limitations. Production adoption must
explicitly own patch maintenance and remove the patch when a suitable maintained
upstream entrypoint is selected and verified.
