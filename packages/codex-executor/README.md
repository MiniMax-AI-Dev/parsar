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
