# Raw remote Files and history qualification

This opt-in fixture uses the pinned upstream `RemoteAppServerClient` over a private
Unix socket and the [qualified manager hook](../raw_manager/README.md). Typed Files
and native execution share the stock-built EnvironmentManager and one authorized
registry/Noise pair. The socket is a local control connection, separate from that
executor pair. No production daemon path or public Files API is enabled.

## Prepare and build

Use the Linux/toolchain/native-resource prerequisites in the manager qualification.
The shared preparation command exports the exact native commit and verifies both
the manager patch and the fixture-only client dependency patch. The latter adds
one dev-dependency/lock edge to an existing exact-pin workspace crate; third-party
packages and versions remain unchanged. The original workspace-version-only lock
normalization is recorded separately. All hashes are in `source.json`.

From the Parsar worktree, with an absolute `NATIVE_SOURCE` Git checkout path:

```sh
export RAW_FILES_ROOT="$HOME/.parsar/raw-files-qualification"
python3 services/agents-api/tests/native/raw_manager/prepare.py \
  --manifest services/agents-api/tests/native/raw_files/source.json \
  --source "$NATIVE_SOURCE" --output "$RAW_FILES_ROOT/source"
mkdir -p "$RAW_FILES_ROOT/state"
export TMPDIR="$RAW_FILES_ROOT/state"
export CARGO_HOME="$HOME/.parsar/cache/agents-native-cargo"
export CARGO_TARGET_DIR="$HOME/.parsar/cache/raw-files-target"
export RUSTUP_TOOLCHAIN=1.95
export CARGO_PROFILE_DEV_DEBUG=0
cd "$RAW_FILES_ROOT/source/codex-rs"
cargo build --locked -p codex-app-server \
  --example parsar_raw_files_probe --example parsar_shared_files_probe
cargo clippy --locked -p codex-app-server \
  --example parsar_raw_files_probe --example parsar_shared_files_probe -- -D warnings
rustfmt --check --edition 2024 app-server/examples/parsar_raw_files_probe.rs \
  app-server/examples/parsar_shared_files_probe.rs
```

Use a new output directory. Keep `preparation.json`, the compiled probe and matching
native helper/launcher hashes, toolchain, command outcomes and failed evidence.
Run the required repository `make check` separately; native builds and model calls
are explicit opt-in acceptance checks.

## Real-provider acceptance

Supply the database, native helper/launcher, pinned executor image, private proof
root and real model key prerequisites in the [native guide](../README.md#shared-native-filesystem-owner).
Point `PARSAR_RAW_FILES_PROBE` at the raw example. From the Parsar worktree:

```sh
go test ./services/agents-api/internal/store \
  -run '^TestNativeRawEnvironmentFiles$' -count=1 -v -timeout=12m
```

The outer Go fixture owns the actual leased database, registry, separate executor
and harness credentials, executor container and independently observed workspace.
It rejects proof directories outside the caller's canonical `~/.parsar` before
creating state. It retains the original caller HOME while giving the native probe
an isolated HOME. Use a short private state/socket path; Unix socket path limits
still apply. A phase uses one native owner and one authenticated pair.

Both first/fresh phases check 128 KiB binary bytes/hash and metadata/listing while
idle and during a real native command, exact remote effects and observed command
output/status, then fresh-process native history with retained files and a
prompt-only random value. The legacy in-process fixture remains available through
`TestNativeSharedEnvironmentFiles` and `PARSAR_SHARED_FILES_PROBE`; run it with the
prepared legacy example when shared fixture code changes.

Both fixtures also read a retained 2 MiB + 37-byte binary through the native
same-manager `read_file_stream`, including a 4096-byte prefix and an empty file.
The checks run while idle, during execution and after cold continuation, preserving
the original whole-file, metadata and execution assertions. The retained result is
bounded, but the native stream may fetch a complete 1 MiB chunk for a short prefix.
Early drop schedules native close; neither it nor EOF proves a close receipt or
settlement of all underlying I/O. These checks do not qualify caller detachment,
path confinement or snapshot consistency for a production Files interface.

The dedicated cancellation workflow preserves that ordinary regression and adds a
separate `first -> cancel -> fresh` run with the same raw example:

```sh
go test ./services/agents-api/internal/store \
  -run '^TestNativeRawEnvironmentFilesCancellation$' -count=1 -v -timeout=12m
```

Go independently observes the active command before allowing native interruption.
The fixture distinguishes its acknowledgement from the observed interrupted Turn,
nullable command completion, targeted background-terminal termination and command
effects. A termination target must match the current Turn's observed native item
and process identifiers; an OS PID never selects a native target. The owner stays
alive while typed Files and the independently observed command exit are checked.
After a fresh process resumes, native Turn listing must retain the interrupted
Turn, and typed Files must retain the post-cancel marker and binary hash before new
execution. Completed command counts prove old work was not rerun. Keep native
typed bodies and actual unknown fields; do not synthesize an exit code or final
answer for cancellation.

## Limits

The maintained remote client has an internal unbounded event channel and uses its
pinned typed notification parser. The fixture's finite event/byte assertions do not
bound that queue or prove preservation of unknown notifications. This workflow
qualifies private Files/execution/history composition, not production backpressure,
complete output, public Files paths/references/pagination, idle ownership or caller
and tenant authorization. Client shutdown alone does not stop the runner; the
fixture must use native runner shutdown and join it before claiming completion.

Only the dedicated three-phase workflow qualifies this cancellation composition;
the ordinary first/fresh checks do not exercise it. Command PID exit and a stable
heartbeat are bounded observations, not proof that all descendants are gone.
Production adoption also requires exact Environment/device/generation ownership, bounded capacity and
lifetime, history retention and stale-write retirement. Never replay an unknown
mutation. Readiness-gated command output does not fix the recorded native early-output
limitation. Real model calls are necessary for this acceptance; no-model or synthetic
checks cannot substitute for it.
