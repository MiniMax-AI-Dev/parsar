# Private Codex harness artifact

`parsar-codex-harness` is an opt-in Linux amd64 executable. It embeds the pinned
Codex raw stdio runner and exposes one private remote metadata operation through
the runner's own `EnvironmentManager`. The existing Go `JSONRPCClient` owns the
child and native execution transport. The metadata socket is local control IPC,
not another executor/Noise connection. Default daemon installation and public
feature admission are unchanged.

## Source and patch ownership

Parsar maintains this integration artifact. It is not the stock upstream binary.
`source.json` pins Codex 0.153.4 at
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`, Rust 1.95.0, the manager hook and a
separate named-binary manifest overlay. The hook's canonical copy is
`patches/manager-exposure.patch`; the older native qualifications reference the
same file. Maintain its qualification and hash with every deliberate change.
Replace the hook when an equivalent maintained upstream entrypoint is selected
and independently accepted; never silently change the native pin.

Preparation exports that exact Git commit, ignoring checkout modifications. It
checks the original lock, normalizes only the 149 upstream workspace package
versions, checks the resulting lock, applies the two hashed patches and injects
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
no local filesystem fallback and no read, write or listing method.

## Acceptance limits

Qualification must use this final binary through the existing Go RPC caller and
an actual authenticated remote executor. Native execution creates a file; metadata
is observed while idle, during execution, after cancellation and following fresh
process history continuation. Controlled tests cover startup/EOF, identity errors,
socket collision, oversized frames, stalled peers and early runner exit. Preserve
failed evidence and distinguish local child exit from remote mutation retirement.

Raw stdio avoids a typed-notification parser and preserves the native transport.
This does not mean the Go adapter stores unknown notifications or that every
existing RPC queue/write path is production-qualified. IPC frame, concurrency and
deadline bounds do not establish general native filesystem resource limits.
Metadata does not prove path isolation, public Files semantics, a reusable idle
owner, Core authority, successor safety or complete output fidelity. These remain
separate admission and acceptance work.
