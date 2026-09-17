# Native directory helper acceptance

`TestNativeExecutorDirectoryHelper` uses a real PostgreSQL registry, its issued
executor/harness credentials, the pinned native client and an actual Docker
executor. It directly invokes the installed helper through native process RPC,
requires the reported Linux sandbox, and waits for exit and output closure.
It checks root metadata, a 5,000-entry directory with bounded output, an empty
directory, symlink ancestry and invalid path/limit rejection. These are mechanism
tests with synthetic files and zero model calls. They do not establish public
Files compatibility, adapter lifecycle integration or real model execution.

Build `probe.rs` as an example of `codex-exec-server` in an isolated export of
commit `3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`, using the repository's pinned
native toolchain and the existing workspace-lock normalization. No native source
or protocol patch is required. Keep sources, build state and evidence below
`~/.parsar/`. Build the production helper with `make build-agents-executor`.

Set the existing private integration-test database URL, `PARSAR_DIRECTORY_PROBE`,
`PARSAR_DIRECTORY_HELPER`, `PARSAR_EXECUTOR_PROOF_DIR`, `PARSAR_EXECUTOR_LAUNCHER`,
`PARSAR_CODEX_BINARY`, and the digest-pinned `PARSAR_PLACEMENT_EXECUTOR_IMAGE`.
The latter prerequisites follow the [native fixtures](../README.md).
Then run:

```sh
go test ./services/agents-api/internal/store -run '^TestNativeExecutorDirectoryHelper$' -count=1 -v
```

The fixture removes its container and temporary credential. Evidence retains no
provider credentials. Directory-descriptor unit tests separately control ancestor
replacement between opens and before enumeration, verify the scan bound, and
check descriptor release. Production consumers must also qualify cancellation,
transport uncertainty, installation trust and their exact owner/authorization
binding before admission. Adapter changes require real model calls before/after
observations through the retained harness; this helper-only fixture is not a
replacement for that acceptance.
