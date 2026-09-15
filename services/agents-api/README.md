# Agents API

Independent execution service implementing part of the pinned OpenAI Agents API.
It owns reusable Agents, durable Sessions/Turns/Items, live events, function actions
and a daemon execution worker. Public execution supports Codex and an opt-in Claude
SDK profile. It builds and runs with its own PostgreSQL database and credentials;
Parsar's product service, frontend and database are not required.

Use this guide to build, configure and connect a client. The
[protocol coverage](../../contracts/agents-api/README.md) lists supported operations,
engine limits, acceptance evidence and missing resources. The target remains the
complete pinned protocol; current workflows do not establish full compatibility.
Parsar product execution and its eventual public-client cutover are separate.

## Reusable Agents

The pinned Python client can save configuration independently of execution:

```python
agent = client.beta.agents.create(model="your-model", name="Example")
session = client.beta.agents.sessions.create(
    agent_id=agent.id, environment={"type": "none"},
)
```

These resources belong to the authenticated execution tenant. Saving configuration
does not launch an engine. Optional Session `agent` fields override the saved
configuration: omitted fields inherit, supplied objects/arrays replace whole fields.
Source and Session metadata stay separate; execution never looks up the source again.

- Retrieve with `client.beta.agents.retrieve(agent.id)`; list saved resources with
  `client.beta.agents.list(limit=20, order="desc")` and SDK auto-pagination.
- Update with `client.beta.agents.update(agent.id, instructions="New instructions")`.
  Omitted fields remain unchanged. Metadata replaces all pairs; null/empty clears it.
  Existing Sessions retain their configuration; new Sessions resolve the update.
- Delete with `client.beta.agents.delete(agent.id)`. Existing Sessions and history
  remain available. New references fail; recorded creation retries recover their
  accepted snapshot without consulting the deleted source.
- List Sessions with `client.beta.agents.sessions.list(agent_id=agent.id)`. Filtering
  uses the immutable root ID, including inline Agents and history after source changes.

See the [configuration and retry limits](../../contracts/agents-api/README.md#public-semantics)
before relying on optional settings or hosted error/default equivalence.

## Build standalone binaries

```bash
make build-agents-api
# Optional absolute output directory:
AGENTS_API_BUILD_DIR="$HOME/.parsar/build/agents-api-test" make build-agents-api
```

The default output is `${PARSAR_HOME:-$HOME/.parsar}/build/agents-api`:

- `agents-api`: HTTP service and execution worker.
- `agents-api-migrate`: this service's embedded database migrations.
- `agents-api-device`: operator device provisioning and revocation.
- `agents-api-environment-key`: principal executor key issuance, rotation and revocation.

Use these executables in place of the corresponding `go run` commands below.
The build needs Go and access to its pinned module dependencies; it does not need
Node, Docker, the product service or frontend. An isolated source context enforces
that boundary on every build. [Contributor rules](../../CONTRIBUTING.md#independent-build-artifacts)
define the allowed shared packages and required checks. Runtime database/key
configuration and a separately installed execution daemon are still required;
these binaries do not establish full protocol coverage. For a standalone Linux
container, see [Container deployment](CONTAINER.md).

`make build-agents-api-release` packages the same four commands in a versioned
Linux amd64 archive, with source/protocol identity, checksums, a license and
[operator instructions](RELEASE.md). Build from a clean Git worktree with Go and
Python 3.9+; output defaults to `~/.parsar/build/agents-api-release` (or
`AGENTS_API_RELEASE_DIR`). The extracted API needs no source checkout or compiler.
Database setup, daemon/native installation and publication remain separate.

## Database ownership

Use a dedicated PostgreSQL database and account, separate from the Parsar product.
This service does not import `server/internal` or apply product migrations.
Migrations are embedded and tracked in `agents_api_schema_version`.

```bash
AGENTS_API_DATABASE_URL='postgres://.../agents_api' \
  go run ./services/agents-api/cmd/migrate
```

The public `Idempotency-Key` creation header is optional: omission creates a new
Session. A supplied key identifies the request within its authenticated tenant.
Inline retries use normalized effective configuration; new saved-Agent references
record caller intent independently of later source updates/deletion. Retries do
not admit initial input again. Every retry must match the original typed creator,
including across key rotation. Another principal using the same project/key gets
the local 409 conflict. Records with a known creator but no recorded request intent
retain resolved-snapshot behavior; records without a creator cannot be retried.
These retry policies are not verified hosted semantics. See the
[retry boundary](../../contracts/agents-api/README.md#public-semantics).

The Store uses internal creation keys and preserves immutable engine/configuration,
native continuity and same-tenant device bindings. The public API applies schema
validation/defaults before storage. Internal bounds are 64 KiB for metadata and
512 KiB for configuration. Keep credentials out of both. Public metadata permits
at most 16 string pairs, 64-character keys and 512-character values; storage bounds
do not replace those rules. Tenant identity comes from authenticated credentials,
never metadata or a caller-supplied business identity.

## Internal Turn persistence

Validated message/cancel/function-result batches commit under a tenant-scoped
Session lock. Messages start a Turn when idle and steer active work. Retry keys
identify the whole ordered batch; an invalid event does not partially admit it.
Queued cancellation needs no live engine. Active cancellation awaits a native
outcome, and completion may win the race. Terminal states cannot be overwritten.

A Session keeps its effective configuration, engine and device across Turns;
product Conversations, native Sessions, connections, processes and sandboxes are
different objects. Strict native resume requires retained history on that device.
The API owns durable public history and pending function decisions; adapters own
native translation and their harness owns the model/tool loop.

The worker uses its database lease connection for execution writes. Lease loss
fences those writes; it does not prove native commands or side effects have stopped.
Restart conservatively fails previously claimed work and retains queued work.
Uncertain delivery is never blindly replayed. Function actions and live SSE are
available within the [current coverage](../../contracts/agents-api/README.md);
other pending interactions, process-loss recovery and environment lifecycle remain
incomplete. Durable acceptance is not an exactly-once side-effect guarantee.

## Standalone HTTP service

Run migrations first, then `go run ./services/agents-api/cmd/server`. The service
requires `AGENTS_API_DATABASE_URL` and `AGENTS_API_KEYS_FILE`; it does not read the
product database or accept product login cookies. The key file is a JSON array:

```json
[{
  "tenant_id": "<canonical nonzero UUID>",
  "organization_id": "<organization ID>",
  "project_id": "<project ID>",
  "subject_kind": "service_account",
  "subject_id": "<stable service-account ID>",
  "token_sha256": "<SHA-256 hex digest>"
}]
```

Use `subject_kind: "user"` for a user principal. IDs are explicit operator-assigned
execution identities, not inferred from Parsar users or existing Session records.
Each key authorizes one project; multiple keys and principals may share that
project's tenant UUID. Startup atomically verifies the immutable organization/project
to tenant mapping before serving traffic or starting execution. Conflicts abort
startup without committing a partial configuration. Removing keys leaves those
mappings intact. Existing key files must be updated explicitly; incomplete legacy
bindings are rejected. This does not assign ownership to historical Sessions.

Provision random bearer keys and share plaintext only with authorized callers;
keep digests in the server file. Rotate or revoke by changing bindings and
restarting the service. Keep the same principal IDs when rotating a caller's key.
Optional `OpenAI-Organization` and `OpenAI-Project` headers must match its binding;
repeated or conflicting values fail authentication. These identities do not grant
product-user rights. New Sessions persist the authenticated creator kind/ID
atomically and never change them on retry. Project resource visibility and mutation
authorization are unchanged. Historical Sessions keep unknown creators and remain
readable; no key, metadata or product record can assign their ownership through a
retry. Retire older API writers before starting this deployment; mixed-version
creation is unsupported. Executor keys separately match this recorded creator before authorizing an
Environment connection; they do not inherit general caller API permissions.

`AGENTS_API_ADDR` defaults to `127.0.0.1:8091`; use a TLS reverse proxy for remote
access. `AGENTS_API_ENGINE` defaults to `codex`; set it to `claude_sdk` for the
registered SDK profile. It selects new Sessions independently of the requested
model. Existing Sessions retain their stored engine.

The SDK base URL is `http://127.0.0.1:8091/v1`. Requests require a bearer key and
`OpenAI-Beta: agents=v1` (set by the official SDK). Supported operations include:

- Saved Agent create/retrieve/update/list/delete.
- Session create/retrieve/list and metadata-only update. Creation supports inline
  configuration or a saved `agent_id`, field replacements, optional initial text
  and ordinary or streaming responses.
- Session event submission and live streaming, Turn retrieve/list and Items list.
- Environment retrieve for the supported self-hosted profile.

Execution uses the selected
[engine profile](../../contracts/agents-api/README.md#public-engine-profiles),
including `none` and the Codex self-hosted idle-text profile described below.
Requests have a 1 MiB body limit. Session lists support `after`, `limit` (1..100),
`order` (`asc`/`desc`) and optional immutable root `agent_id`. The local defaults
are 20 and descending order; exact hosted limits/error semantics remain unverified.
Metadata updates preserve omission, clear on null/empty and replace supplied pairs.

Delete with `client.beta.agents.sessions.delete(session.id)`. Confirmation means
public removal: Session/history reads and new input become unavailable. Active
work receives a cancellation request; existing streams close on observing removal.
Already claimed work may still complete. Creation keys stay reserved; deletion
never affects other Sessions, saved Agents or their shared device. Internal records
and native history are retained for execution settlement; physical cleanup remains
unimplemented. Local repeated deletion returns 404 and creation-key reuse returns
409; exact hosted errors and overlapping stream timing are unverified.

Non-text message input, Vaults, Subagents, Environment files/templates and populated
installation metadata remain unsupported. Saving optional Agent configuration does not make
it executable. Unsupported requests fail explicitly. `/healthz` reports liveness only.

## Internal execution device connection

The standalone service can accept existing daemon connections without a Parsar
workspace or product database. Enable its internal gateway by setting
`AGENTS_API_DAEMON_WS_URL=wss://your-service/api/v1/agent-daemon/ws` (use `ws`
for local development). This is separate from the official Agents API executor
contract; do not return this URL as a public `self_hosted` environment's remote URL.

After migrations, an operator can provision a device for an execution tenant:

```bash
umask 077
mkdir -p ~/.parsar/parsar-daemon/agents-api
go run ./services/agents-api/cmd/device \
  --tenant '<execution-tenant-uuid>' --name 'local executor' \
  --url 'http://127.0.0.1:8091' \
  > ~/.parsar/parsar-daemon/agents-api/auth.json
parsar-daemon connect --profile agents-api
```

The command requires `AGENTS_API_DATABASE_URL` and emits a secret profile once.
Use a new profile rather than overwriting an existing device's credentials. When
provisioning remote compute, securely transfer this file to the same profile path
on the executor. The database stores only the credential digest. API keys and
device credentials are not interchangeable. To revoke a device:

```bash
go run ./services/agents-api/cmd/device \
  --tenant '<execution-tenant-uuid>' --revoke '<device-uuid>'
```

An existing connection is retired on its next heartbeat; new connections are
rejected immediately. The internal Store binds each Session to one same-tenant
device, preserves that assignment across retries/restarts, and refuses a silent
move to another device. Revoked bindings cannot be used for dispatch. Device
connections alone do not start a Turn. Submit text, cancellation or function results
through the official Session events endpoint; the worker assigns a same-tenant host and preserves that
binding. Provider lifecycle remains under construction. See the [ownership rules](../../CONTRIBUTING.md#product-and-execution-service-separation).

### Enable Claude SDK execution

Build and extract the runtime archive into a fresh managed directory on a matching
executor host. The archive contains the compiled bridge and pinned production
SDK/MCP/native dependencies; Node is installed separately. Linux x64/glibc with
Node22 is the accepted platform. See the
[runtime artifact contract](../../CONTRIBUTING.md#private-claude-sdk-runtime-artifact)
for build outputs, version checks and platform restrictions.

```bash
make build-claude-sdk-runtime
# After extracting the matching archive into this operator-chosen directory:
export PARSAR_CLAUDE_SDK_ENTRYPOINT="$HOME/.parsar/runtimes/claude-sdk/dist/main.js"
export PARSAR_CLAUDE_SDK_NODE="/absolute/path/to/node"
parsar-daemon connect --profile agents-api
```

Set `AGENTS_API_ENGINE=claude_sdk` on the API service. Configure provider access in
the daemon's private native SDK environment. Runtime readiness checks versions and
startup before daemon registration; it does not validate provider credentials.
SDK state stays under the daemon profile, independently of the replaceable bundle.
A ready SDK can start the daemon without a legacy CLI. Product `claude_code` remains
separate. Managed Node installation, runtime activation and registry publication
are not supplied by these commands.

## Official client verification

After preparing a dedicated test database, build the server and verify it with
the official client installed from the commit in `contracts/agents-api/upstream.json`:

```bash
python -m pip install -r services/agents-api/tests/requirements.txt
make build-agents-api
AGENTS_API_SERVER_BIN="${PARSAR_HOME:-$HOME/.parsar}/build/agents-api/agents-api" \
  python services/agents-api/tests/official_client.py
```

The test uses `PARSAR_AGENTS_API_TEST_DATABASE_URL`, temporary service keys and
fresh tenant IDs. The suite checks upstream and generated response schemas, retries,
ordering, tenant isolation, unsupported options and reads after a process restart,
without a model provider. It also runs the
[official Go client integration](../../packages/agents-client/README.md), using two
fresh tenants, and validates its created Sessions through the Python SDK.

## Checks

```bash
PARSAR_AGENTS_API_TEST_DATABASE_URL='postgres://.../parsar_agents_api_local_tests' \
  make check-agents-api
```

Set `PARSAR_OFFICIAL_SDK_PYTHON` to the fixed SDK interpreter for the Store client
fixtures, and run the separate official-client command above as well.
`TestEnvironmentRetrievalOfficialClient` verifies public creation, scoped safe
Environment reads and retrieval after reopening without execution configuration.
`TestEnvironmentInitialFailureOfficialClient` verifies failed Session reads and
matching SDK/raw live failure events after privately provisioned initial input
expires, without fabricating a Turn or changing the Environment status. It is a
persistence prerequisite test. `TestSelfHostedInitialCreationOfficialClient`
separately exercises ordinary/streamed public initial creation through the Worker,
retry identity, disconnect survival and explicitly controlled deadline failure.
The test database must be named `parsar_agents_api_*_tests` and contain no product
workspace tables. Tests apply only this service's migrations and use new tenant
IDs without truncating tables. Missing test configuration skips DB tests locally;
the `agents-api` CI workflow always supplies its own PostgreSQL service. Run the
full `make check` before review as well. Product OpenAPI generation excludes this
service; its supported HTTP contract is generated separately.

## Public text execution

With the daemon gateway enabled, the service owns one worker per execution database
and processes up to four Turns concurrently. Other workers are rejected by a
PostgreSQL advisory lock. A disconnected host leaves unsent work queued; clients
may cancel it. Session/Turn/Items queries expose durable results.

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8091/v1", api_key="<execution-key>")
session = client.beta.agents.sessions.create(
    agent={"model": "<model-available-on-the-engine-host>"},
    environment={"type": "none"},
)
client.beta.agents.sessions.events.create(
    session.id,
    events=[{"type": "agent.session.input.message", "input": [
        {"role": "user", "content": [{"type": "input_text", "text": "Hello"}]}
    ]}],
    idempotency_key="first-message",
)
```

Configure model access in the engine host's native configuration. API tenant keys
and daemon credentials authenticate this service, not a model provider. Never put
provider secrets in Session metadata. This path does not enable Parsar Skill/SP
callbacks or bypass the pending product authorization work.

Session creation also accepts `input="Hello"` to admit initial text atomically and
`stream=True` for created/live events. Open a GET event stream before submitting
later work, or use the official `sessions.stream` helper for one Turn. Function
handlers return results through the same public events endpoint. Recover missed
output with Session/Turn/Items queries; reconnecting SSE does not replay history.
See [creation streaming](../../contracts/agents-api/README.md#session-creation-streaming)
for retry behavior and unverified hosted timing.

The [accepted workflows](../../contracts/agents-api/README.md#acceptance-evidence-and-remaining-scope)
include real MiniMax execution through built API/daemon/Codex and Claude SDK,
function success/error, cancellation and native continuation. Controlled fixtures
remain useful but do not replace real-provider acceptance for execution changes.

## Upgrading archived Item history

Migration 15 retires private journal-to-Item backfilling. It preserves existing
public Items and source journals, and refuses to apply if any Turn still has
`items_indexed=false`. Do not set this marker manually or replay native execution.

For installations with pre-Items history:

1. Back up the execution database and stop new execution/submission. Drain active
   Turns before switching versions. Product storage is independent.
2. Run the previous service release `906069e` against the execution database with
   its worker disabled (omit `AGENTS_API_DAEMON_WS_URL`). Using each tenant's API
   credential, list every Session and request its Items once. The old service
   prepares the complete index under the Session lock, even with `limit=1`.
3. Verify `SELECT count(*) FROM turns WHERE NOT items_indexed` returns zero.
   A failed preparation must be resolved before upgrade; legacy journals cannot
   recover fields they never recorded. Stop the previous service.
4. Upgrade the daemon first so it advertises `tool_observations`, then apply the
   migrations and start the new service. No old/new service overlap is supported
   across this migration. Devices without this capability are not dispatched.

For step 2, use the pinned Python SDK and the usual private endpoint/key settings,
repeating with each operator-configured tenant identity:

```python
from openai import OpenAI

client = OpenAI()  # OPENAI_BASE_URL and OPENAI_API_KEY
for session in client.beta.agents.sessions.list():
    client.beta.agents.sessions.items.list(session.id, limit=1)
```

Fresh installations and already indexed history need no backfill. Recovery reads
continue to use Session/Turn/Items; this procedure is an upgrade operation, not
an official SSE replay mechanism.

## Native executor transport prerequisite

The disabled-by-default Codex adapter supports executor registration, harness key
authorization and an opaque native Noise relay. Configured execution and an
executor origin enable public `self_hosted` Sessions on Codex. The current
profile requires an absolute `workspace_directory`, empty/default
`capability_directories` and optional supported non-deferred functions. Initial text is optional.

To enable it alongside the existing daemon worker, set
`AGENTS_API_EXECUTOR_URL` to the externally reachable HTTPS origin. Apply the
execution migrations first and start the service once with configured caller
principals to establish its immutable project mappings. Operator database authority
can then issue a connect-only principal key before any Session exists:

```bash
umask 077
agents-api-environment-key --tenant "$TENANT_ID" \
  --organization "$ORGANIZATION_ID" --project "$PROJECT_ID" \
  --subject-kind service_account --subject-id "$SUBJECT_ID" --key-id "$KEY_ID" \
  > "$HOME/.parsar/executor-key.json"
```

`KEY_ID` is a new canonical nonzero UUID chosen for management and retained by the
operator. Use `--subject-kind user` for a user principal. All identities must be
explicit and match an existing verified project mapping; issuance never creates
or remaps that association. `AGENTS_API_DATABASE_URL` points to the execution DB.
Optionally add `--environment "$ENVIRONMENT_ID"` at issuance to restrict this key
to one existing live Environment with the same recorded Session creator.

JSON output contains `key_id`, `executor_token`, and `environment_id` only for a
restricted key. Stdout is its only delivery; the
[separate native launcher](../../packages/codex-executor/README.md) consumes this
private file directly. There is no chosen-token import or secret read-back.
Ordinary issuance rejects any existing management ID, including revoked IDs.
Lost output requires explicit `--rotate` with the same full principal and key ID;
`--revoke` invalidates the key without output. Neither operation accepts an
Environment override or changes the key's principal/restriction. Rotation of a
restricted key requires its live owning Session; revocation remains possible after
deletion. Replace the private file and restart executors after rotation.

The database stores the current digest, immutable typed subject/project partition,
optional restriction, creation/issuance times and revocation marker. Authorization
requires the target Session's project and recorded creator to match. A principal
key can serve multiple matching Sessions; deleting one denies that target without
revoking access to the others. Unknown historical creators never authorize an
executor. Caller, daemon, harness and executor keys have distinct purposes.
Executor keys have no five-minute grant expiry. A restart preserves keys but
invalidates registrations and URL grants, requiring re-registration. Current-key
checks apply to requests and upgrades; authorization heartbeats close existing
pairs every five seconds with a four-second check budget. Connection closure does
not establish native process quiescence.

**Principal-key cutover:** stop older registries and operator writers, apply
migration 26, and deploy the updated service, issuer and launcher together. Legacy
digests, Environment restrictions and issuance times remain, but keys are revoked
and their principals remain unknown. Their Environment UUIDs remain reserved as
management IDs; they cannot be claimed or rotated into principal keys. Explicitly
issue new keys with new IDs, replace old files and restart executors. Never infer
ownership from old keys or product data. Downgrade refuses to discard principal-key
identities and never undoes legacy revocation. The retired static executor-key
setting remains rejected; there is no old/new authentication fallback.

Harness credentials are issued internally for an execution owner after checking
the current execution lease and exact tenant/Environment ownership. The registry
holds only bounded process-local digests. The owner retains its credential through
preparation and the transferred Run, then releases it; cancellation and service
shutdown also revoke access and close that credential's pair. Connection tickets
still expire after five minutes, independently of the active owner's lifetime.
See the [canonical ownership rules](../../CONTRIBUTING.md#environment-ownership-and-placement).

**Transition from static harness keys:** stop the old registry, remove
`AGENTS_API_HARNESS_KEYS_FILE`, retire its secrets/files and restart. That setting
now fails startup rather than retaining a static fallback. With the daemon gateway
and executor URL configured, the service Worker issues and releases these credentials
for pending Environment inputs, selecting a capable tenant device once for an
unbound Session and retaining existing bindings. There is no public harness-key endpoint or user/service-account
identity equivalence. Caller, device, executor and harness credentials stay separate.

Native routes live outside `/v1/agents`: `POST /cloud/environment/{id}/register`
uses the executor credential; `/connect` uses the harness credential and `/validate`
uses the executor credential. The returned WebSocket URLs carry separate connection
capabilities. Keep URLs and `harness_key_authorization` private; redact query
strings in external access logs. This native adapter does not expand the pinned
public SDK OpenAPI surface. HTTP is
allowed only on loopback for development. Production TLS termination remains an
operator responsibility and requires deployment validation.

Authenticated socket observations now persist connection state and immutable
Environment event snapshots. These observations also back resource status reads,
without establishing native readiness. Registration replacement
and numbered callbacks fence old observations; a new Worker reconciles previous
process state before opening connections. Shutdown drains observations before
releasing execution ownership. Persistence failures close the registry and require
a service restart; review its lifecycle error logs rather than treating closure as
a successful write. See the [lifecycle rules](../../CONTRIBUTING.md#environment-ownership-and-placement).

When the executor origin is configured, Session GET/list/metadata and live SSE can
expose `self_hosted` Sessions through a safe output projection. Waiting input requests `environment_connection` before any Turn;
connection arrival clears the action, and the existing Worker still verifies
native readiness before admission. No waiting input means no connection request.
The returned `remote_url` is the configured executor origin. Use that exact URL and
Environment ID with the [pinned caller-started launcher](../../packages/codex-executor/README.md).
Later idle text-message batches wait for durable preparation/admission before the
input endpoint returns 204, even if the executor is already connected. Set client
and proxy timeouts above five minutes; the service gives this response six minutes.
Explicit retry keys preserve the original input identity and deadline. A disconnected
HTTP observer does not cancel the reservation. Local expiry/cancellation errors are
409 `environment_input_expired` / `environment_input_cancelled`; ownership loss is
503 `execution_unavailable`. Exact hosted status/body parity remains unverified.
Initial text on ordinary or streamed creation commits its reservation and returns
the connection target promptly while offline. Connect using that target; the same
Worker prepares and starts the initial Turn. A disconnected creation stream does
not cancel the reservation. Initial expiry leaves a queryable failed Session with
a safe error and no Turn; exact hosted error/timing parity remains unverified.
Cancellation-only events use the existing durable receipt path, including idle
no-Turn requests and retries that never retarget later work. A new cancellation
still conflicts with pending pre-Turn input. HTTP 204 acknowledges admission;
observe completion through events/queries and process cessation separately.
Function definitions use the existing callback bridge. Submit result-only batches
with explicit Turn/call identities; they create no Turn or preparation and retain
the same identities on retries after completion or during later work. Pending
reservations still block new results. Observe native application through the
existing actions, Items and events; admission alone does not acknowledge application.
Message-only batches append to an existing active Turn or reserve idle work under
the same Session lock. Active input returns after durable admission and uses the
existing native steering receipts; retries keep their original Turn after completion
or during later work. No unlocked activity check can bypass idle preparation.
Mixed events, deferred functions,
nonempty capability directories, other placements, populated installation metadata
and Environment file/template routes remain unavailable. These are implementation gaps.

Retrieve the returned Environment with
`client.beta.agents.environments.retrieve(session.environment.id)`. This read uses
durable status and the owning live Session's project authorization, even when
execution/registry configuration is disabled. Its required `files`, `plugins` and
`skills` arrays are empty for the supported configuration, which has no API-managed
installations. They do not list caller-prepared or model-created workspace files,
or report native capability discovery. Unsupported stored installation configurations
are rejected rather than reported as empty. The response contains no credentials,
private configuration or file contents. See the
[resource boundary](../../CONTRIBUTING.md#environment-ownership-and-placement).

The adapter checks its execution lease and visible Environment on requests and
five-second heartbeats; deleted ownership, shutdown or lost ownership closes
connections. Re-registering replaces an old socket without allowing its late close
to clear the replacement. A live credential can register again after replacement.
No command replay or native process termination is promised.

Each Environment currently accepts one independent harness connection. Multiple
native commands, processes and file operations share it. A second attachment
receives 409 without evicting the incumbent; `/connect` refresh remains
non-disruptive. Five-minute, one-use harness grants bind both keys and sockets to
the current registration. At most 32 grants are retained per registration;
expired unused grants are pruned, and exhaustion returns 429. Expiration prevents
new attachment/validation without terminating an established pair.

The relay forwards binary frames unchanged, bounded to the native 256 KiB limit.
A stalled write closes the pair after five seconds, without an application queue.
Either peer disconnecting closes both physical sockets and invalidates outstanding
harness grants. Native recovery may resume a retained Session/process; the service
does not restart commands. Multiplexing independent harnesses, durable native
backup, deployment TLS and arbitrary interrupted-work recovery remain unverified.

The [native probe](tests/native/relay_probe.rs) exercises commands, a 128 KiB file,
refresh, one retained process across a controlled outage, and fresh file retention.
Build it as the `parsar_relay_probe` example in the pinned Codex Rust workspace
(`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`), with its matching lock/dependencies.
The release source's workspace version labels may need alignment to its manifests;
do not change third-party dependencies. Keep build/runtime files under `~/.parsar/`.
Run `TestNativeHarnessRelayPostgreSQLAndProcessRecovery` with the dedicated execution
test DB, `PARSAR_CODEX_BINARY` (0.153.4), `PARSAR_NATIVE_RELAY_PROBE` (that example)
and `PARSAR_EXECUTOR_PROOF_DIR` (private output directory). Without those native
prerequisites that test skips; the regular authorization/relay/Store checks still
run. This transport proof makes zero model calls; public model execution through
Environment remains a separate required acceptance workflow.

The pinned Codex 0.153.4 CLI accepts registry API-key authentication on loopback but
protects OpenAI credentials from third-party production domains. The separately
named [upstream-library launcher](../../packages/codex-executor/README.md) provides
an explicit service-credential path. It keeps the stock guard intact and does not
establish the documented stock command on an arbitrary production domain.
See [Environment contracts and remaining work](../../contracts/agents-api/environments.md).
