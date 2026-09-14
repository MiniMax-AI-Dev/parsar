# Native Environment probes

These fixtures exercise pinned native execution against the actual Agents API
registry/relay and a dedicated execution PostgreSQL database. They are opt-in;
ordinary CI skips native binaries and paid model calls when prerequisites are absent.
The standalone public fixture below exercises the initial `self_hosted` text
profile. The other probes exercise private adapter boundaries. None establishes
complete Agents API compatibility; use the protocol coverage ledger for those gaps.

## Public standalone self-hosted execution

Build the standalone service with `make build-agents-api`. Supply the pinned SDK,
native daemon, Codex 0.153.4, executor launcher, private proof directory, dedicated
execution test database, local Docker image and real MiniMax credential described
below, then run:

```sh
export PARSAR_AGENTS_API_SERVER_BIN="$HOME/.parsar/build/agents-api/agents-api"
export PARSAR_OFFICIAL_SDK_PYTHON='<absolute Python from the pinned SDK environment>'
go test ./services/agents-api/internal/store \
  -run '^TestNativePublicSelfHostedStandalone$' -count=1 -v -timeout=12m
```

`official_self_hosted.py` creates empty Sessions through ordinary and streamed
official-client requests, then submits both text inputs through the built server.
The first request must remain open beyond the ordinary HTTP write timeout while
there is no executor or daemon. No Turn or Item may exist during that wait. The
caller starts the executor using the returned Environment ID and `remote_url`
unchanged; the registered daemon and Worker perform preparation and admission.
The second input starts with a connected executor and retained native history.
Both real model Turns must execute the exact remote command with observed cwd,
stdout/stderr, exit 7, retained files and remembered first-Turn context. SDK and
independent raw SSE observers verify activity ordering, responses, query recovery,
tenant isolation and retries without additional Turns or commands.

Private fixture writes provision only operator identity/device credentials, never
Sessions or input reservations. Store reads independently check execution identity
and native command evidence. Unsupported initial input, functions and mixed input
must fail before persistence. This fixture does not cover all Environment resources,
hosted providers, public cancellation, process isolation or unknown-effect recovery.
It incurs real provider usage and must run serially with other execution-lease
tests on Linux. Passing evidence is produced only by an actual successful run,
under `PARSAR_NATIVE_PROOF_DIR/public-self-hosted-*`.

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

With the same prerequisites, run `TestNativeDaemonPreparedRemoteEnvironment` to
exercise private prepare/ready/start through the registered daemon. Its separate
preparation subscription creates no Run, then the actual Run starts using the
returned handle. This repeats real remote command/file, cold-history and
cancellation acceptance; it does not enable public Environment admission. Controlled
subprocess/router tests establish deferred-start ownership independently. Both
variants obtain their harness credential from the current registry under the
fixture's execution owner; after real execution, they release it and verify that
native preparation rejects the former credential. No static harness-key file is
used. Public caller principal identity and Worker admission remain separate work.

Run `TestNativePreparedWorkerRemoteEnvironment` with the same prerequisites to
exercise the real Worker above these controls. It reserves input without a
Turn, selects and binds a capable daemon, resolves a live harness credential,
prepares that daemon, atomically claims the original batch and persists ordinary
events/completion. Two real model
Turns verify remote instructions, exact command cwd/output/exit, retained files and
cold native history. Repeated reservation submission must return replay receipts
without allocating credentials or executing another command. Controlled Store and
gateway tests separately cover preparation failure, expiry/deletion/cancellation,
control-only Start rejection and cancellation while Start is pending. This fixture
configures the Worker resolver before startup. It additionally requires
`PARSAR_OFFICIAL_SDK_PYTHON` pointing to the fixed SDK in `upstream.json`.
Strict SDK and independent raw HTTP/SSE observers subscribe before reservation,
verify the connection action without Turns/Items, and supply the returned URL and
Environment ID unchanged to the caller-started executor. Scheduling begins after
that offline snapshot and initial connection; native readiness, promotion and both
Runs remain Worker-owned. The observers verify action clearing before the first
Turn, both completed Turns/Items, tenant isolation and recovery through a new
client. Private evidence includes `public-environment/public-environment-proof.json`.
Session provisioning and input reservation stay private in this fixture; the
standalone fixture above owns public creation/input acceptance. Complete
Environment lifecycle remains separate work.
The current daemon's pending-start cancellation acknowledgment may omit Outcome;
controlled coverage verifies conservative failure without final Done as well
as cancellation with a supplied outcome. It does not claim complete native
pending-start cancellation results or immediate process quiescence.

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
admission, complete lifecycle, files/templates and broader resource projection still
need their own implementation and real acceptance.

## Separate executor launcher

Build `agents-api-codex-executor` with `make build-agents-executor`, and compile
the current `relay_probe.rs` against the pinned upstream libraries as described
in the [service guide](../../README.md#native-executor-transport-prerequisite).
Then supply the ordinary PostgreSQL/native/image prerequisites above plus:

```sh
export PARSAR_EXECUTOR_LAUNCHER="$HOME/.parsar/build/agents-executor/agents-api-codex-executor"
export PARSAR_NATIVE_RELAY_PROBE='<absolute matching relay probe executable>'
go test ./services/agents-api/internal/store   -run '^TestNativeExecutorLauncherTLSAndHelpers$' -count=1 -v -timeout=6m
```

This fixture uses controlled Docker DNS and a test CA with actual HTTPS/WSS.
It rejects an untrusted CA and wrong hostname before registry HTTP handling,
then verifies native commands, a 128 KiB file, connection recovery, fresh reads,
native filesystem/argv0 helper modes, read-only enforcement and graceful launcher
exit. It mounts the full native installation with its resources. Docker's outer
seccomp/AppArmor restrictions are relaxed solely so the native sandbox can run
inside the test container; this is not a production isolation recipe or public
DNS/certificate deployment. Model calls are zero.

With `PARSAR_EXECUTOR_LAUNCHER` set, `TestNativeDaemonRemoteEnvironment` uses the
same new launcher and full native installation instead of stock CLI registration.
Its provider calls remain actual MiniMax. The provisioned executor JSON is the
only permitted persistence of that executor credential; harness credentials stay
transient. This second fixture uses loopback HTTP and separately verifies the
authenticated daemon, cold history/files and cancellation. Neither fixture enables
public `self_hosted` admission or proves complete Environment compatibility.

The prepared Worker fixture (`TestNativePreparedWorkerRemoteEnvironment`) requires
the built launcher and issues a principal key before creating its Environment
Session. It verifies the serialized key ID and absent exact restriction before
real-provider commands/files and cold continuation. PostgreSQL/HTTP fixtures cover
same-principal multiple Sessions, cross-principal rejection, rotation/revocation,
restart and deletion. These checks do not enable public Environment admission.
