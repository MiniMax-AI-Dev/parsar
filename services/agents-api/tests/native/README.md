# Native Environment probes

These fixtures exercise pinned native execution against the actual Agents API
registry/relay and a dedicated execution PostgreSQL database. They are opt-in;
ordinary CI skips native binaries and paid model calls when prerequisites are absent.
They do not admit public `self_hosted` or establish complete Agents API
compatibility. The registered-daemon probe below exercises the private adapter
boundary; public Environment dispatch remains separate. Use the protocol coverage ledger for those gaps.

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

## Registered daemon adapter

`TestNativeDaemonRemoteEnvironment` sends the typed remote descriptor through a
real authenticated daemon/gateway, using the same registry and executor. It creates
harness credentials after daemon startup, so preloaded transport environment cannot
satisfy the test. The fixed native version must advertise the capability through
its actual heartbeat. Supply the placement prerequisites above plus:

```sh
export PARSAR_NATIVE_DAEMON_BIN='<absolute daemon built from this checkout>'
export PARSAR_NATIVE_PROOF_DIR="$HOME/.parsar/daemon-environment-proof"
mkdir -p "$PARSAR_NATIVE_PROOF_DIR"
chmod 700 "$PARSAR_NATIVE_PROOF_DIR"
go test ./services/agents-api/internal/store \
  -run '^TestNativeDaemonRemoteEnvironment$' -count=1 -v -timeout=12m
```

The fixture explicitly sets daemon `PARSAR_CODEX_BIN` to `PARSAR_CODEX_BINARY`.
It checks invalid transient authorization, remote instructions/cwd/output/exit/files,
release and same-thread cold continuation, then sends `prompt_cancel` during an
actual remote command. PID exit and stopped heartbeats are observed independently
while the daemon, registry and executor remain running; the measured cleanup delay
is recorded, with no immediate-quiescence claim. Provider usage is real MiniMax-M3.

The Environment token must be absent from persisted files and responses. The device
credential remains in its profile; the existing provider adapter may store its key
in private harness `config.toml`. Neither is mounted into the executor. The explicit
shell policy checks inheritance, not arbitrary same-user process visibility.
`remote-adapter-proof.json` describes this bounded daemon acceptance; public input
admission, durable lifecycle, credentials, files/templates and API projection still
need their own implementation and real acceptance.
