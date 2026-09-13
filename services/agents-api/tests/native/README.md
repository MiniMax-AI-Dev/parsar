# Native Environment probes

These fixtures exercise pinned native execution against the actual Agents API
registry/relay and a dedicated execution PostgreSQL database. They are opt-in;
ordinary CI skips native binaries and paid model calls when prerequisites are absent.
They do not admit public `self_hosted`, dispatch through the daemon, or establish
complete Agents API compatibility. Use the protocol coverage ledger for those gaps.

`relay_probe.rs` is compiled as an example of the pinned `codex-exec-server` crate.
`TestNativeHarnessRelayPostgreSQLAndProcessRecovery` runs it against the Go registry.
It uses synthetic scoped credentials and zero model calls.

`environment_model_probe.py` drives the unmodified Codex 0.153.4 **app-server** over
stdio. The harness owns the model/tool loop; this script only sends user Turns and
observes native events, real files and processes. It calls the real MiniMax-M3
Responses API, never a mock endpoint. Run it through the Go test, which owns the
tenant, Environment, scoped synthetic credentials and execution lease:

```sh
export PARSAR_AGENTS_API_TEST_DATABASE_URL='<dedicated parsar_agents_api_*_tests URL>'
export PARSAR_CODEX_BINARY='<absolute native Codex 0.153.4 executable, not JS wrapper>'
export PARSAR_EXECUTOR_PROOF_DIR="$HOME/.parsar/placement-proof"
export PARSAR_PLACEMENT_EXECUTOR_IMAGE='sha256:<local Debian image digest>'
export PARSAR_PLACEMENT_MODEL_KEY_FILE="$HOME/.parsar/secrets/minimax.key"
mkdir -p "$PARSAR_EXECUTOR_PROOF_DIR"
chmod 700 "$PARSAR_EXECUTOR_PROOF_DIR"
go test ./services/agents-api/internal/store \
  -run '^TestNativeAppServerRemoteModelPlacement$' -count=1 -v -timeout=12m
```

Run on Linux with Python 3.10+, Docker, a loaded Debian image containing Bash and
coreutils, and the native executable compatible with that image. The fixture does
not pull images or install packages. Optional HTTP(S) proxy variables are forwarded
to the harness; loopback registry traffic bypasses them. Serialize this test with
other execution-lease/database tests. Model calls incur provider usage.

The executor has its own container PID/filesystem view and mounts only its private
state, test workspace and read-only native executable. Provider credentials and
harness history stay outside it. Host networking is used for the loopback registry;
this is neither network isolation nor a production deployment recipe. Generated
code still shares the executor's user/process visibility. A scoped executor token
must not be confused with a caller, device or provider credential.

The probe requires explicit remote selections, verifies a workspace absent on the
harness host, checks remote instructions and actual command output/exit/files, and
cold-resumes the same native thread from retained harness history. It records that native interruption preserves the demonstrated background command,
then targets that Turn's process through native list/terminate RPCs and independently
observes PID exit and stopped heartbeats. A second configuration records only the
names of exposed credential variables under the native default policy. Secrets are
checked before evidence is saved. The test removes only its owned container and
processes; private evidence/history remain under the configured evidence directory.
Other binary versions, native credential lifetime, unknown-effect recovery, production
TLS/domain authentication and full API/daemon lifecycle need separate acceptance.

A successful probe reports `characterized_with_blockers`: its native behavior
assertions passed, while public dispatch/cancellation integration remains unimplemented.
It is not a passing claim for the complete Environment feature.
