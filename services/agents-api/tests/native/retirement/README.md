# Native executor retirement qualification

This opt-in Linux regression distinguishes connection/task shutdown from retirement
of already admitted filesystem work. It is a negative qualification of the pinned
Codex executor, not a production fix or public Files acceptance.

## Reproduce

Use the [raw Files prerequisites](../raw_files/README.md) and the same exact native
source/toolchain. From the Parsar worktree:

```sh
export RETIREMENT_ROOT="$HOME/.parsar/retirement-qualification"
python3 services/agents-api/tests/native/raw_manager/prepare.py \
  --manifest services/agents-api/tests/native/retirement/source.json \
  --source "$NATIVE_SOURCE" --output "$RETIREMENT_ROOT/source"
mkdir -p "$RETIREMENT_ROOT/state"
export TMPDIR="$RETIREMENT_ROOT/state"
export CARGO_HOME="$HOME/.parsar/cache/agents-native-cargo"
export CARGO_TARGET_DIR="$HOME/.parsar/cache/retirement-target"
export RUSTUP_TOOLCHAIN=1.95
export CARGO_PROFILE_DEV_DEBUG=0
cd "$RETIREMENT_ROOT/source/codex-rs"
cargo test --locked -p codex-exec-server --lib retirement_qualification -- --nocapture
cargo clippy --locked -p codex-exec-server --tests -- -D warnings
rustfmt --check --edition 2024 exec-server/src/retirement_gate.rs \
  exec-server/src/server/retirement_qualification.rs
```

The test uses the existing processor's duplex JSON-RPC helper and typed native
`fs/writeFile` parameters with `followSymlinks: false`. A separately hashed,
`cfg(test)`-only gate pauses one existing blocking worker after it opens and
validates a regular file, immediately before the original truncation/write. It
changes scheduling, not the mutation algorithm. The gate matches one exact path,
releases on test unwind and has a 15-second timeout. The owned shell heartbeat is
bounded even if an assertion fails. This test does not exercise sandboxed writes,
all filesystem operations or every platform.

In both cases the test observes the original worker entering, closes the connection
and joins its handler, verifies the mutation has no response, then admits a new
native owner writing the same regular file. The successor's acknowledged bytes
must be present before releasing the original worker. The original bytes then
replace them. One case additionally awaits the actual `ConnectionProcessor`
shutdown and observes the owned command PID disappear before admitting the
successor. Command retirement and blocking filesystem work are separate facts.
No unknown mutation is replayed.

Successful test completion means this precise missing barrier was reproduced.
It does **not** mean retirement passed. If upstream behavior changes to settle
the held worker, re-evaluate the assertions and guarantee rather than weakening
the test to keep the negative result.

## Retirement matrix

| Boundary | Evidence and limit |
|---|---|
| Connection handler return | Instrumented native regression: the admitted blocking write remains live, and the owned command continues after detach. |
| `ConnectionProcessor::shutdown` return | Instrumented native regression: the owned command exits, while the held write can still overwrite a successor. This is not whole OS-process or Tokio-runtime destruction. |
| Native remote runner | Source: `remote.rs` calls the processor shutdown after the remote transport ends. This test exercises that processor boundary directly, not an end-to-end remote runner shutdown. |
| Registry pair disconnect / harness credential withdrawal | Source: the registry closes authenticated physical peers; native remote reconnect retains its processor. Transport closure is not a filesystem settlement receipt. Credential withdrawal is not directly injected by this test. |
| Core execution lease loss | Source: database ownership controls service admission; it cannot retract work already dispatched to a native executor. No lease-loss injection is claimed. |
| Executor process / isolated placement destruction | Not qualified here. A future implementation needs a verified supervisor/storage boundary or native mutation-drain receipt before successor writes. |

The replacement owner in this regression is admitted directly to native processors;
it is deliberately not evidence that public Core admission allows this race. It
shows why that admission must not infer write retirement from these boundaries.
Retain unresolved ownership and block successor mutation when the actual retirement
barrier is unknown, including recovery after Core restart. Do not equate connection
observation generations with a filesystem fence or change the public protocol.

## Separate real execution acceptance

The manifest reuses the manager hook and raw Files fixtures without changing their
native pin or third-party dependencies. The extra gate and test module compile
only in the native library unit-test build. Build the ordinary raw Files example
and run its existing first/fresh and cancellation workflows with a real model API,
the existing registry, pinned launcher and executor image. Record artifact/source
hashes separately from the instrumented library test and retain failures. Those
workflows verify actual Files, native commands and preserved history; they do not
upgrade this mechanism result into production retirement acceptance.

Run repository `make check` independently. No API or database query is changed.
Public Files, production owner integration, complete descendant retirement and
Claude's independent placement qualification remain separate tasks.
