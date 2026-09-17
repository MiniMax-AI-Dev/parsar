# Private Codex harness artifact

`parsar-codex-harness` is an opt-in Linux amd64 executable. It embeds the pinned
Codex raw stdio runner and exposes private remote metadata and bounded reads through
the runner's own `EnvironmentManager`. The existing Go `JSONRPCClient` owns the
child and native execution transport. The file socket is local control IPC,
not another executor/Noise connection. Default daemon installation and public
feature admission are unchanged.

## Source and patch ownership

Parsar maintains this integration artifact. It is not the stock upstream binary.
`source.json` pins Codex 0.153.4 at
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`, Rust 1.95.0, the manager hook and a
separate named-binary manifest overlay. A separately hashed bounded-read patch
exposes one native operation without exposing the general RPC client. The hook's canonical copy is
`patches/manager-exposure.patch`; the older native qualifications reference the
same file. Maintain its qualification and hash with every deliberate change.
Replace the hook when an equivalent maintained upstream entrypoint is selected
and independently accepted; never silently change the native pin.

Preparation exports that exact Git commit, ignoring checkout modifications. It
checks the original lock, normalizes only the 149 upstream workspace package
versions, checks the resulting lock, applies the hashed patches and injects
`src/` into `codex-rs/app-server/parsar-harness/`. The named binary uses existing
app-server dependencies. No dependency resolution, client dependency or
third-party version change is part of the overlay. Mismatched identities fail.

## Build and checks

Install Rust 1.95.0 with rustfmt and Clippy, Python 3.10+, Git, tar, a C toolchain,
pkg-config and OpenSSL development headers on Linux amd64. Supply an existing
official Codex Git checkout containing the pinned commit:

```sh
export AGENTS_HARNESS_NATIVE_SOURCE="$HOME/.parsar/references/codex-native"
make check-agents-harness
make check-agents-harness-native
make build-agents-harness
```

`make check` includes the lightweight packaging checks. The explicit native check
prepares a fresh export and runs the binary's locked unit tests, formatting and
Clippy. Native checking and a release build are required for artifact changes;
they are intentionally separate from the ordinary local gate. CI runs both on
affected paths. Real executor/provider acceptance is separate from all build checks.

Builds and caches stay below `~/.parsar`. `CARGO_HOME`, `CARGO_TARGET_DIR` and
`AGENTS_HARNESS_BUILD_DIR` may override their defaults only within that root.
`RUSTUP_TOOLCHAIN` may select an installed alias; the build verifies that its
compiler reports exactly Rust 1.95.0.
The default output is `~/.parsar/build/agents-harness/parsar-codex-harness` beside
`provenance.json`. Provenance records the native commit, manifest, patches,
injected sources, prepared lock, toolchain and artifact hash. Acceptance must also
record the exact stock helper hash and check execution without the prepared source
tree present. Build provenance alone does not establish runtime compatibility.

## Private startup contract

The operator supplies these environment variables to the child:

| Variable | Meaning |
| --- | --- |
| `PARSAR_CODEX_HARNESS_NATIVE` | Absolute path to the stock native 0.153.4 helper |
| `PARSAR_CODEX_HARNESS_ENVIRONMENT` | One canonical remote Environment UUID |
| `PARSAR_CODEX_HARNESS_WORKSPACE` | Absolute workspace path on that executor |
| `PARSAR_CODEX_HARNESS_IPC_ROOT` | New private directory below the caller's `~/.parsar` |

The wrapper accepts the existing `-c` overrides and `app-server --stdio` with
`--enable`/`--disable` features. Unsupported options fail explicitly. Native
configuration and `CODEX_HOME` remain native concerns; provider credentials must
not be added to wrapper arguments. Public requests cannot select local process,
helper or socket targets.

The endpoint is `files.sock` within the new `0700` IPC directory, with mode `0600`
and a same-UID peer check. Existing directories or socket paths are not overwritten.
Each bounded connection carries one JSON line with `environment_id` and a relative
`path`. The identity must match startup configuration, and the manager entry must
be remote and ready. Startup also matches the operator UUID to the native registry
Environment variable. The native manager uses its fixed `remote` key, independently
of that UUID. A response reports native metadata or a safe error. There is
no local filesystem fallback. Omitting `operation` selects metadata. An explicit
`operation: "read"` requires an integer `max_bytes` from 1 through 8 MiB; this is a
private adapter bound, not a public protocol limit. The response has `read` with
base64 `data_base64`, `truncated` and `close_acknowledged: true`. Reads use native
blocks of at most 1 MiB and one extra byte to distinguish an exact-bound file from
a truncated prefix. No snapshot consistency is promised for a changing file.
An explicit `operation: "list_directory"` requires `max_entries` from 1 through
4096; an empty relative `path` selects the workspace root. It uses the native
walk at depth zero with a result limit and native no-follow metadata in a read-only
filesystem permission context rooted at the bound workspace. `directory` contains `entries`
(single-component `name`, `kind`, and `size_bytes` for regular files) and explicit
`truncated`. The pinned walk omits symlinks and non-regular entries; later metadata
may observe a type change. This live private observation is not a snapshot or
complete public Files semantics. The native implementation collects and sorts all
names before applying its entry limit: this bounds responses, not enumeration work
or memory. Large-directory resource qualification remains open. Native errors do
not return a partial success; ambiguous transport failures stop this owner without
claiming remote cleanup or admitting another read. There is no write method.

The bounded-read hook captures one native RPC connection before opening a handle.
Open, ordered block reads and cleanup use that exact connection without recovery
or replay. A successful result requires a successful close response after all
reads. A server rejection settles that particular request; it does not establish
successful close. Ambiguous transport/protocol outcomes and unconfirmed close
stop the owner without delivering partial bytes or admitting another request.
Closing a replacement connection cannot settle an old handle. The native stock
stream remains unchanged; its asynchronous Drop cleanup is not used as a receipt.

Startup freezes the operator binding before calling native `arg0_dispatch`. This
preserves native `CODEX_HOME/.env` credential loading and helper dispatch before
threads start, without letting dotenv replace private selectors. The native alias
guard lives until runtime teardown; explicit child re-execution uses the pinned
stock helper.

The request shares one ten-second deadline. A stalled frame or response writer
closes its connection. Caller disconnect does not cancel an admitted native wait.
When the raw runner ends, stop new admission and pending frames, then drain the
admitted operation within its original deadline. A stopped owner need not deliver
the result to the caller. Preserve runner failures after a successful drain, and
report an unresolved drain as failure even if the runner exited normally. This
only accounts for the native future; it does not prove remote effect retirement.
The existing daemon RPC `Close` can force-kill this child after its 250 ms grace,
interrupting the drain. A daemon/Core file consumer must reconcile that boundary
and retain uncertainty before treating release as operation settlement; this
artifact change does not alter the RPC's existing local-reap contract.
If the native operation has not settled by the deadline,
the artifact exits with an error and closes admission; dropping the native wait
does not cancel remote work. Recovery must retain that uncertainty and must not
infer remote retirement from this local failure. Runtime shutdown waits at most
one second for blocking tasks, including native stdin, so a caller keeping its
input pipe open still observes local process exit. This is not a remote cleanup
guarantee.

## Acceptance limits

Qualification must use this final binary through the existing Go RPC caller and
an actual authenticated remote executor. Native execution creates a file; metadata
and bounded bytes are observed while idle, during execution, after cancellation
and following fresh process history continuation. Synthetic binary and empty files
supplement native-created files to check byte fidelity, exact bounds and truncation.
Each successful read must include its ordered close acknowledgment. Controlled tests cover startup/EOF, identity errors,
socket collision, oversized frames, stalled peers and early runner exit. Preserve
failed evidence and distinguish local child exit from remote mutation retirement.

Raw stdio avoids a typed-notification parser and preserves the native transport.
This does not mean the Go adapter stores unknown notifications or that every
existing RPC queue/write path is production-qualified. IPC frame, concurrency and
deadline bounds do not establish general native filesystem resource limits.
Private metadata and byte reads do not prove path isolation. Directory access needs
separate actual native permission/isolation acceptance. None of these observations
establishes public Files semantics, a reusable idle
owner, Core authority, successor safety or complete output fidelity. These remain
separate admission and acceptance work.

## Opt-in adapter launch

Set `PARSAR_CODEX_HARNESS_BIN` to the absolute integrated artifact path and keep
`PARSAR_CODEX_BIN` pointing to the stock helper. The daemon uses the artifact only
for validated remote Codex preparations. It supplies the frozen binding and a new
private socket directory, retains the existing Prepared/Session/RPC lifecycle,
and cleans the directory after the child is reaped. Nonremote Codex and Claude
keep their current launch paths. No wrapper or new public capability is required.
The private file socket remains operator infrastructure; public Files and
Core ownership/retirement gates are separate work.
