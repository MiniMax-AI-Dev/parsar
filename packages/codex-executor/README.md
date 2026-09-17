# Native Codex executor for Agents API

`agents-api-codex-executor` is a separately named launcher for a third-party Agents
API registry. It embeds the unmodified Codex 0.153.4 executor libraries from commit
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`. Upstream owns registration, Noise,
reconnection, files, process execution and graceful shutdown. This package owns
explicit connection configuration and its service-issued credential.

This is an optional Linux x86_64 component. The Agents API service and other daemon
adapters build independently. It does not enable public `self_hosted` admission or
establish full Environment compatibility. Stock `codex exec-server` retains its
API-key domain restriction; this launcher does not change that command.

## Build and installation

Install Rust 1.95.0 with rustfmt and Clippy, a C toolchain, pkg-config and OpenSSL
development headers. Build with the committed Cargo lock:

```sh
make check-agents-executor
make build-agents-executor
```

Build state stays under `~/.parsar/`. `CARGO_HOME`, `CARGO_TARGET_DIR` and
`AGENTS_EXECUTOR_BUILD_DIR` can select existing absolute cache/output locations.
The build copies only this package into its build context; no product or API
service code is needed. The resulting GNU binary requires compatible glibc and
OpenSSL runtime libraries. This is not a portable musl artifact.

Install the exact official native Codex 0.153.4 package separately. Keep its
platform resource directory intact, including any bundled sandbox helper.
`--codex-bin` must point to its actual native executable, not the npm JavaScript
entry point. The launcher checks its version and creates a stable
`codex-linux-sandbox` alias under its private state directory. Native filesystem,
argv0 and sandbox helper modes execute that official binary; none are copied into
the launcher. System/container sandbox permissions must support the requested
native policy. Do not interpret an unsandboxed command test as sandbox validation.

## Scoped directory helper

The build also emits `agents-api-codex-directory` for the current directory-listing
adapter gap. Install it at an operator-controlled absolute path on the executor,
outside the writable workspace. Its three argv values are the authorized absolute
workspace root, a relative directory (empty for the root), and a limit of 1–4096.
Invoke it directly through the existing authenticated native process API with a
read-only filesystem policy and restricted network. No shell or model is involved.
The helper itself is a local program, not an authorization service: the caller
must bind the root to the exact authorized owner and validate the installation.

It opens every directory component without following symlinks, retains directory
descriptors for enumeration and metadata, and stops after the limit plus one
entry. It returns one version-1 JSON response: `directory.entries` contains
`name`, `kind` (`file`, `directory`, `symlink`, `other`) and nullable `size_bytes`;
`directory.truncated` reports lookahead. Only regular files have sizes. Errors
use `error` with no partial entries. Invalid paths/names and oversized responses
are rejected. Descriptor cleanup precedes output; exit zero alone is not success,
since a settled error also returns JSON. Require valid complete JSON, a successful
exit and native output-close receipt before accepting an observation. Unknown
start/termination/transport results remain uncertain and must not be retried as
settled reads.

This one-level observation has no order, paging or snapshot guarantee. Renames may
leave an operation reading the directory it already opened; workspace replacement
and cross-tenant placement remain the caller's responsibility. The helper does
not change stock native filesystem methods, create a daemon connection, or enable
public Files. The [native fixture](../../services/agents-api/tests/native/directory/README.md)
qualifies the standalone helper independently of later adapter/public wiring.

## Scoped file installer

The optional `agents-api-codex-write` helper addresses two pinned native write
limitations: hard-link targets are modified in place, and base64 encoding a
50 MiB file exceeds the native 64 MiB message bound. Install this helper outside
the writable workspace and invoke it directly through the native process API,
with restricted network and the required helper/runtime reads. The qualified
installer policy grants write access to one dedicated per-Environment parent
containing only workspace and staging; ordinary native tools can write only the
workspace. Keep credentials, native history and other Environments outside that
parent. Separate writable mount entries can make rename fail with EXDEV even
when their backing filesystem matches; the helper must reject that layout.
It does not authorize callers or enable Files.create.

Arguments are the authorized absolute root, a nonempty relative file path,
its declared byte count (0–50 MiB), and an existing absolute staging directory.
The staging directory must be outside the workspace, not its ancestor, and on
the destination filesystem. Symlink traversal and cross-filesystem replacement
are rejected; there is no copy fallback. The former three-argument private CLI
is no longer accepted. Stream those bytes in bounded native stdin
chunks, followed by their 32-byte binary SHA-256 digest. This is one private frame;
there is no second request on that process. The native process protocol has no
stdin-close method, so the digest terminates the frame without waiting for EOF.
Extra bytes after the frame are not consumed. A process/write accepted receipt
means queued input, not committed file contents.

The helper reuses held-directory no-follow traversal, rejects existing nonregular
targets and requires an existing parent. It writes a fresh mode-0600 temporary
file in the held staging directory with existing rustix openat/renameat operations,
checks the declared byte count and digest, syncs the file, then replaces the
destination directory entry and syncs both directories. Existing hard links retain
their original inode and contents. This
private replacement policy does not preserve destination mode/ownership metadata
or establish official overwrite semantics. The operator must protect staging and
its ancestors from native tools and background processes. A dedicated staging
directory per Environment, with native tool write access limited to the workspace
and separate installer access, is the qualified mechanism. Directory naming or
mode 0700 alone does not isolate processes running as the same user. The helper
cannot verify other processes' policies; public admission must bind and validate
this condition. Broad read permission may still expose staging bytes; confidentiality
requires its own placement policy. Concurrent workspace changes do not gain access
to protected staging, but later writers can change the installed file. No snapshot
or exactly-once guarantee is implied.

One version-1 JSON response reports `outcome: completed` with `size_bytes`,
`failed` before replacement, or `unknown` if either directory sync fails after replacement.
Errors contain only a fixed safe code. Require a complete response plus observed
native exit/output close; exit zero alone is insufficient. Input errors preserve
the old destination provided staging remains protected; independent workspace
writers can still change that destination themselves. Temporary-file cleanup
is best effort: permission or I/O errors, as well as forced termination, can leave
a `.parsar-upload-*` file in the private staging directory. Never interpret it as a
completed upload.
A missing receipt remains unknown and must not trigger automatic replay. This
helper does not fence a replacement owner after remote transport or service loss;
public admission still needs operation ownership and recovery handling.

## Connect an executor

An operator creates an executor principal key with
[`agents-api-environment-key`](../../services/agents-api/README.md#native-executor-transport-prerequisite).
The key may be issued before a Session exists, or optionally restricted to one
existing Environment. Redirect its JSON output to a mode-0600 regular file under
`~/.parsar/` and transfer that credential to its executor. Keep caller, database,
daemon and model-provider credentials outside this compute.

```sh
~/.parsar/build/agents-executor/agents-api-codex-executor \
  --remote https://agents.example.com \
  --environment-id "$ENVIRONMENT_ID" \
  --credentials "$HOME/.parsar/executor.json" \
  --codex-bin /opt/codex/bin/codex
```

The URL is an explicitly trusted service endpoint. HTTPS uses native certificate
and hostname validation; HTTP is accepted only for loopback development.
Userinfo, query strings and fragments are rejected. Native custom CA support uses
`CODEX_CA_CERTIFICATE` or `SSL_CERT_FILE`; no certificate verification bypass is
provided. Native HTTP(S) proxy behavior is retained.

The JSON requires a canonical nonzero UUID `key_id` and the issued 43-character
base64url `executor_token`. The optional `environment_id` may be omitted or null
for a principal key. If present, it must be a canonical UUID equal to the requested
Environment. The server authorizes the key's stored principal and restrictions;
file metadata supplies local validation only.

The credential is read once at startup, without ambient OpenAI login/API-key
fallback or raw secret command-line arguments. At the cutover, update the launcher
and replace old credential files together; files without `key_id` are rejected.
Rotate the key through the operator command, replace the private file, and restart
this launcher. Revocation closes the authorized connection through registry
checks; it is not immediate process quiescence.

Executor state and helper aliases live under
`~/.parsar/codex-executor/<environment-id>/` (or the absolute `PARSAR_HOME`).
The native executor's `CODEX_HOME` is scoped there independently of a user's Codex
login. Run the executable under an ordinary service supervisor. SIGINT and SIGTERM
ask the native library to shut down its sessions/processes before returning.
Generated code shares this executor's process user and filesystem visibility;
a private credential file or separate directory is not filesystem isolation.

## Verification boundaries

`make check-agents-executor` checks configuration, scoped credential handling,
formatting and Clippy. Native transport, TLS, sandbox/helper behavior and actual
model calls require the opt-in [Environment fixtures](../../services/agents-api/tests/native/README.md).
Record their prerequisites and results separately; unit tests and successful
linking alone do not establish a usable executor deployment.
