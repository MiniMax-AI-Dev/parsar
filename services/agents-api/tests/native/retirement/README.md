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
  exec-server/src/server/retirement_qualification.rs \
  exec-server/src/server/placement_qualification.rs
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
| Executor process / isolated placement destruction | Qualified separately by the candidate fixture below for its task-owned local Docker placement. Production admission still needs an authorized supervisor/storage boundary or native mutation-drain receipt. |

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

## Whole-placement candidate qualification

`placement.py` uses the same exact-pin test build and scheduling gate in a
credential-free, task-owned Linux Docker unit. Build the `codex-exec-server`
library test binary with `cargo test --locked -p codex-exec-server --lib --no-run`,
then pass that binary and the already qualified immutable executor image:

```sh
python3 services/agents-api/tests/native/retirement/placement.py \
  --binary "$NATIVE_TEST_BINARY" --image "$EXECUTOR_IMAGE_ID" \
  --output "$HOME/.parsar/placement-retirement/attempt-1"
```

The host must be the Docker host, expose readable cgroup v2 membership/events,
and use the existing Debian executor image with `setsid`. The test process is
the placement init; the native processor dispatches a held file write and a
command that starts a detached-session descendant. Before stopping, the runner
checks native readiness, actual cgroup members and independent process-session
identity. A still-live placement fails the retirement observation and cannot
start the successor. Docker stop is followed by cgroup and process-identity
observations before the fresh native write; a successful stop request alone is
insufficient. Failed assertions retain evidence and reclaim only exact labeled
test instances. Workspace files survive container removal.

This qualifies only the tested local filesystem and placement. It does not
implement Core authority, durable unknown-owner reconciliation, remote supervisor
receipts, Claude containment or public Files. Whole-placement retirement does not
undo completed effects; the held old mutation remains unknown and is never
replayed. No model credentials enter the instrumented unit. Run the ordinary
real-model Files/cancellation/history fixture separately, and report its outcome
independently. Production runtime/pins and the earlier negative tests are unchanged.

## Local Runtime operator consumer

Build the existing daemon and pass `--controller /absolute/path/to/parsar-daemon`
to `placement.py` to exercise the actual local retirement command. The same held
native write and detached descendant are stopped through that consumer. Separate
CLI processes race on the binding and recover the identical receipt after removal;
the fixture independently checks old processes, retained successor bytes and an
untouched neighboring container. Scoped enrollment includes a generated Environment
UUID; wrong, missing and explicitly empty scopes must fail without stopping the
placement. This is operator-confirmed association, not Core resource validation.
Earlier native negative tests remain unchanged.

Operators explicitly create an owned container with
`--label parsar.runtime.placement=<owner>`, then run:

```sh
parsar-daemon placement enroll --container "$FULL_CONTAINER_ID" \
  --owner "$PLACEMENT_OWNER" --workspace "$ABSOLUTE_HOST_WORKSPACE" \
  --environment "$ENVIRONMENT_ID"
parsar-daemon placement retire --container "$FULL_CONTAINER_ID" \
  --environment "$ENVIRONMENT_ID"
```

This initial profile requires local Linux/cgroup v2 and the fixed socket
`unix:///var/run/docker.sock`; ambient Docker context/host variables do not select
the target. Use a non-root container user, private PID/IPC/cgroup namespaces,
`--network none --cap-drop ALL --security-opt no-new-privileges --restart no`,
no devices, additional capabilities, shared volumes or privileged settings.
Exactly one writable bind retains workspace/history on ext-family, XFS, Btrfs or
tmpfs storage without nested mounts; additional binds may only be read-only regular
files. Every source must be on a whole-filesystem host mount with exactly one mount
for that device in the controller namespace. Host bind aliases, Btrfs subvolume
roots, repeated-device or stacked mounts and missing mount evidence are rejected;
this first profile does not resolve arbitrary backing-path aliases. Controller state and the canonical Docker socket must not be exposed by any mount,
including ancestor directories and filesystem roots. Use canonical absolute
workspace paths and a trusted operator account with Docker access. State ancestors
must be owned by that user or root and not writable by group/others; the placement
state directory and files require modes 0700/0600. Host administrators remain trusted.

The command persists intent before stop and verifies stopped state, cgroup emptiness
and old process identities before non-forced container removal. It never removes
workspace files or Docker volumes, releases an ordinary Turn, or replays unknown
writes. A saved completed receipt survives controller restart; recovery completes the
directory-sync barrier before returning success. Unavailable evidence,
changed incarnations and removal without a durable receipt remain unknown. An
interrupted stop can reconcile the same still-existing stopped unit. There is no
clear-unknown shortcut. This consumer does not gate Core dispatch or grant public
feature admission; remote authority, Claude placement and broader storage remain
separate work. Run full `make check` and uninstrumented real-provider acceptance
separately from the credential-free mechanism fixture.
