# Environment contract and implementation path

This assessment covers the fixed [Python SDK contract](upstream.json). It is an
implementation plan with partial current coverage. Public execution admits
`environment.type=none` on Codex and Claude SDK, the Codex self-hosted text/function
profile, and operator-configured Docker/E2B hosted profiles for Codex, Claude Code
and MiniMax Code below.
Environment retrieval supports safe metadata for these environment profiles;
populated startup installations and templates remain missing. Live file listing
and local inline/source writes have [partial coverage and explicit local policies](environment-files.md).
See [current coverage](README.md#public-semantics).

The internal Store now owns a durable Environment association for newly created
`self_hosted` and `openai_hosted` snapshots, atomically with Session creation.
It derives configuration and tenant ownership from the Session; retries preserve
the existing identity. Scoped reads hide associations after Session deletion while
retaining the underlying record. The public text profile reuses this association
and the preparation/admission path below; additional provider profiles remain open.
Missing/`none` configurations and historical internal snapshots gain no backfill.


The native Codex registry uses principal executor digest bindings with optional exact-Environment
restrictions, the existing execution owner and scoped Store reads. Registration and current
socket identity are process-local; the returned WebSocket capability expires for
new connections after five minutes. Restart invalidates registrations, causing the
native executor to register again. Replaced socket callbacks cannot clear a newer
connection. Current socket observations now commit `connected`/`disconnected` and
immutable pinned Environment-event snapshots through the leased Store. Replacement
and revision fencing prevent late observations from overwriting successors; startup
reconciliation removes the previous process's connection evidence. Registration
alone is not connection, and connection is not native readiness. The public text
profile and resource reads use this bridge. The canonical
[observation and shutdown rules](../../CONTRIBUTING.md#environment-ownership-and-placement)
cover write failures and recovery. Deleting the owning Session rejects new requests and closes
existing sockets on the next ownership heartbeat. A previous holder of a still-valid
executor credential can register again; permanent exclusion requires revocation.
Execution owners now obtain transient harness credentials through the internal
registry after exact tenant/Environment and execution-lease authorization. Their
owner context spans preparation and the transferred Run; release/cancellation
invalidates the credential and its own grants/pair. Static harness keys are retired.
These credentials obtain short-lived, key-bound connection grants.
The relay pairs one harness with the current executor socket and forwards native
binary frames unchanged. Either peer loss closes both physical connections and
invalidates grants; no queued frames or commands move to a successor. Refresh does
not disturb a healthy pair. See the [operator prerequisite](../../services/agents-api/README.md#native-executor-transport-prerequisite).

## Basic public Docker-hosted profile

An explicitly configured default managed Docker provider enables `type=openai_hosted`
for the qualified Codex, Claude Code and MiniMax Code profiles. Each uses the same
Runtime lifecycle and workspace interfaces with its own native adapter/isolation.
See the [engine profile guides](README.md#public-engine-profiles) for setup and limits.
The standalone [operator configuration](../../services/agents-api/deploy/codex/README.md#standalone-operator-configuration)
selects the qualified immutable Runtime image; advertised capabilities alone do
not enable admission. An idle or initial-text creation commits Session, Environment
and retry identity before the existing leased Worker provisions its allocation.
A committed creation interrupted before bootstrap is recovered without replaying
an existing allocation's Create.

Omitted/null network defaults to enabled; explicit enabled and disabled use the
same image with adapter-selected immutable native policy. Unsupported restricted
domains, templates, populated env/packages/setup/files/plugins/skills/capability
paths fail explicitly. Empty/null installation defaults produce safe empty metadata,
not a live workspace inventory. Hosted MCP combinations remain unimplemented.

Initial provisioning leaves a Session idle until a Turn starts, with no caller
connection action. The managed scan records authenticated, exactly bound daemon
connections through existing fenced generations. Native preparation remains
separate. Core restart preserves allocation/workspace/native identity; it does not
blindly replay uncertain work. Terminal cleanup revokes authority and settles
pending input atomically before external reclamation. Matching retries preserve
outcomes; new inputs reject terminal Environments. Expiry has no invented SSE
variant. Local failure codes and exact event ordering remain unverified upstream
semantics; this profile does not establish complete Environment compatibility.

## Basic public E2B-hosted profile

The [E2B operator configuration](../../services/agents-api/deploy/e2b/README.md)
selects a qualified immutable template/build for the same three harnesses and
`type=openai_hosted` admission. It retains the shared Runtime execution, Files,
Artifacts and recovery paths and the public configuration limits above. Actual
[three-harness E2B acceptance](README.md#e2b-v1-qualification) is separate from
Docker evidence. The Provider's five operations manage allocation, initialization,
lease renewal and cleanup only. A minimum two-hour renewable lease is required;
expiry destroys volatile VM workspace/history and cannot authorize replay or
transparent recreation. Pausing, migration and user-managed enrollment are not
part of this qualified profile.

## Initial public self-hosted profile

Create a Session with `environment.type=self_hosted`, an absolute
`workspace_directory` and omitted/null/empty `capability_directories`. Creation
accepts initial text as a string or ordered user-message array. Omitted/null input
creates no Turn or connection action. Configured execution, a validated registry origin,
Codex and supported non-deferred function definitions are validated before persistence.

Initial text commits a reservation and connection action, then returns the Session
and Environment connection target while offline. Streamed creation sends its
original `created` snapshot before the connection action. The existing Worker
prepares and admits the input; closing the stream leaves committed work intact.
Initial expiry leaves a failed Session, safe error and empty actions without a
Turn or an Environment failure. Creation retries preserve the original identity,
deadline and input. Later live observers do not replay creation events.

Later text-only batches recover their original reservation or direct receipt under
the Session lock. New active messages append to the current Turn through existing
ordered admission and native delivery; they create no Turn, preparation or reservation.
If no Turn is active, reserve and wait for the existing Worker to retain native
preparation, admit and claim. Return 204 only after that
transaction commits. Connection actions precede a Turn and clear on connection;
connection alone is not readiness. Retries preserve identity and the five-minute
database deadline. HTTP disconnect retains the reservation. Local expired/cancelled
outcomes return 409, lost ownership 503, and deletion 404; exact hosted error
status/body and pending-input crash recovery are unverified. See the
[canonical wait rules](../../CONTRIBUTING.md#environment-ownership-and-placement).

Cancellation-only batches use the existing locked admission and native delivery.
An idle cancellation creates no Turn, and retry identity preserves the original
target during later work. Pending pre-Turn reservations still block new cancellation.
HTTP 204 confirms admission, not native completion or OS quiescence. A cancellation
before native Session transfer can still lack a final Outcome and conservatively
fail; complete cancellation settlement remains open.
Homogeneous function-result batches reuse scoped locked admission and native
application receipts, without creating a Turn or preparation. Definitions use the
existing function parser and remain fixed through native preparation and continuation;
output/error field presence and ordered content keep their existing semantics.
Retries retain the original call, including during later work. New results cannot
bypass pending input. Function callbacks do not populate Environment installations.
Mixed events, non-text input, nonempty capability directories and other
engine placements are rejected temporary gaps. The current adapter also rejects
workspace paths containing NUL, CR, LF or backslash; broader path/platform support
remains open. Native execution still uses the
scoped upstream-library launcher; arbitrary-domain stock CLI support is not proven.
The built-service acceptance must publicly create and submit, keep a request open
past 30 seconds, and verify two real remote command/file/history Turns through
fixed SDK, raw HTTP and live SSE. Private setup alone is insufficient.

## Contract inventory

Paths below follow the SDK resource methods, before the service's `/v1` prefix.
The authoritative fields and unions are linked to the pinned source; this table
is an inventory, not a replacement schema.

| Resource | Operations | Contract distinctions |
| --- | --- | --- |
| [Environment](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/environments.py) | `GET /agents/environments/{id}` | Created through Session configuration, with no standalone create/list/update/delete method in this resource. Safe metadata includes files, plugins, skills, type and status. |
| [Template](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/templates.py) | `POST`, `GET /agents/environments/templates`; `GET`, `POST`, `DELETE /agents/environments/templates/{id}` | Reusable hosted configuration, resolved for each Session. List uses `after`, `limit` and `order`. Supplied update fields replace their value; omitted fields stay unchanged. Deletion includes confidential inputs. |
| [Files](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py) | `POST`, `GET /agents/environments/{id}/files` | Create accepts `file_id` or inline base64 data with an absolute path inside `/workspace`. List uses opaque `page`, not `after`, with stable path/order/limit across pages. |

These are eight operations, separate from Session creation and live events.
Templates use an `after` cursor and limit 1–100, default 20; file listing has nullable
limit/path, non-null order/page when supplied, and case-sensitive path-component
ordering. Both default to descending order. Do not reuse cursor decoding merely
because both endpoints paginate.

[Session environment input](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment_param.py)
and [output](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment.py)
have different shapes:

- `none` selects no execution environment.
- `self_hosted` input requires `type` and `workspace_directory`; its optional
  nullable `capability_directories` defaults to an empty list. `remote_url` is
  output-only. The output also includes the Environment ID and capability paths.
  The output description's `/workspace` default does not make the input field
  optional.
- `openai_hosted` can reference a template and supply capability paths, network,
  packages, files, plugins, skills, environment variables and setup commands.
  Omitted template-backed values inherit; Session overrides cannot broaden the
  template network policy. The service implementing this discriminator owns
  provisioning; it does not rename the public discriminator for its provider.

[Template responses](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/environment_template.py)
expose safe metadata, retaining unresolved skill version selectors and file
references. They do not return inline file/archive contents, environment variables
or setup command bodies. Nullability and replacement behavior must be checked
against each request type, not inferred from these response models.

| Projection | State vocabulary |
| --- | --- |
| [Environment resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environment_info.py) | `pending`, `connected`, `disconnected`, `expired`, `failed` |
| [Session environment event state](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agent_session_environment_state.py) | `pending`, `ready`, `connected`, `disconnected`, `failed`; nullable error |

These projections cannot share an unchecked string cast. Session environment
notifications carry Session/Environment identity and optional Turn identity.
The Session's `required_actions` union includes `environment_connection` with an
Environment ID, separately from function calls. Environment readiness is distinct
from Session and Turn status.

## Ownership and placement decision

The canonical [architecture rules](../../CONTRIBUTING.md#environment-ownership-and-placement)
keep Environment lifecycle common while leaving process placement and native
transport to adapters. Logical ownership does not require a machine per object.

| Object | Responsibility |
| --- | --- |
| API Session and Environment | Durable tenant ownership, configuration, pending interaction and connection observations. |
| Provider allocation | Compute and filesystem lifetime; caller-owned for `self_hosted`, service-owned for hosted provisioning. |
| Device and daemon connection | Authenticated engine-host identity and replaceable internal dispatch transport. |
| Harness process and native Session | Native model/tool loop, execution state and proven history/continuation path. |
| Executor connection | Access to an Environment's filesystem/process capabilities, independently authorized. |

A co-located daemon/harness/workspace is a proposed placement for engines with
native local tools. A harness using a separate executor is another placement.
Neither proposal establishes public compatibility by itself. Advertising the
specified `self_hosted` flow requires an actual caller-started executor to work;
quietly requiring an extra Parsar daemon installation changes that flow.

Co-location needs a real credential and isolation design: generated code must not
gain the broader application credential or cross-tenant secrets through a shared
unrestricted process account. A directory binding alone is not isolation. Retain
native history independently of disposable compute, or prove native restoration;
never treat an Environment ID as a filesystem or history backup. Do not silently
move an existing Session away from its bound device.

## Evidence and interoperability gap

The current [self-hosted guide](https://developers.openai.com/api/docs/guides/agents-api/environments/self-hosted)
uses a restricted executor key and both returned values:

```sh
codex exec-server --remote "$REMOTE_URL" --environment-id "$ENVIRONMENT_ID"
```

Keep the broader application key outside that environment. The returned URL is
passed unchanged on reconnect. Each Session has its own Environment ID/executor.
The guide currently installs `@openai/codex@alpha`; it does not pin our native
binary version. Guide observations are supplemental evidence, not a silent SDK or
engine upgrade.

The pinned native reference is Codex `rust-v0.153.4`, commit
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`:

- [Registry messages](https://github.com/openai/codex/blob/3d2ee51ca2d5db578f328aa75e20aa22c0197c9a/codex-rs/exec-server/src/environment_registry.rs)
  and [remote client](https://github.com/openai/codex/blob/3d2ee51ca2d5db578f328aa75e20aa22c0197c9a/codex-rs/exec-server/src/remote.rs)
  define executor registration, harness-key connection authorization and validation,
  with native `noise_hybrid_ik_v1` transport. Reuse upstream clients and encryption;
  implement the missing registry/relay without rebuilding native execution.
- The [CLI authentication check](https://github.com/openai/codex/blob/3d2ee51ca2d5db578f328aa75e20aa22c0197c9a/codex-rs/cli/src/main.rs)
  restricts API-key registration to HTTPS OpenAI domains or loopback. A third-party
  production URL with a service-issued restricted key is therefore not established
  by this binary. Resolve a supported upstream path explicitly; do not disable its
  credential protection or present local development routing as deployable support.
  The public `RemoteEnvironmentConfig::new` accepts a native `SharedAuthProvider`:
  the [separate launcher](../../packages/codex-executor/README.md) embeds upstream
  execution with an explicit service-scoped credential file. This is a library integration, not a custom-auth flag for the stock CLI
  or proof that its documented command works on a third-party production domain.
- The [native Environment manager](https://github.com/openai/codex/blob/3d2ee51ca2d5db578f328aa75e20aa22c0197c9a/codex-rs/exec-server/src/environment.rs)
  already consumes a registry configuration. Its private settings remain inside the
  Codex adapter. `CODEX_EXEC_SERVER_URL` is a harness-side direct transport selector;
  it is not the public registration URL or the daemon gateway URL.

Recorded binary evidence: the earlier stdio probe initialized the executor,
observed command output and exit 7, and received a termination acknowledgement.
The remote registration preflight now confirms third-party-domain rejection and a
loopback POST containing native security-profile/public-key fields and the supplied
synthetic bearer. The probe deliberately returns 503 before relay allocation.
Those preflights did not establish registration success, Noise interoperability or
process termination. Their evidence remains under
`~/.parsar/remediation/20260913/environment-contract/` on `zju_a100_2`.

A subsequent loopback probe uses the unmodified Codex 0.153.4 executor command,
the matching native `EnvironmentManager`, upstream registry message types and the
upstream opaque relay test helper. It verifies registration/connect/validation,
native remote file write/read, separate stdout/stderr with exit 7, and termination
of a running sleep process through a closed native result with exit 137. A fresh
connection reads the retained file and repeats execution. The relay carries native
encrypted frames; file contents are absent from captured frames. Invalid harness
key authorization, an executor credential used for harness connection and an
unknown Environment ID are rejected.

This is synthetic-credential protocol verification with zero model calls. It does
not test a valid foreign tenant, production WebSocket authorization/TLS, isolated
filesystems, interrupted-command replay or public API/daemon execution. It leaves
the stock CLI's third-party-domain restriction unresolved. Evidence and runnable
fixture sources are retained under
`~/.parsar/remediation/20260913/environment-executor-interoperability/` on
`zju_a100_2`. The upstream release lock needed only local workspace version labels
aligned to its manifests; third-party versions, sources, checksums and dependency
edges stayed unchanged. Native execution and encryption sources were unchanged.

The probe supports reusing the native Codex client/executor libraries for this
adapter. The service adapter now combines durable ownership reads, bounded
registration, scoped harness grants and opaque paired forwarding. A separate
real-PostgreSQL/native test uses the unmodified executor and matching
`EnvironmentManager` against the actual Go adapter. It verifies simultaneous
commands and a 128 KiB file, stdout/stderr/exit, termination, non-disruptive same-key
refresh, and recovery of the same process handle after one controlled transport
outage. A single command-start marker proves that this acknowledged-start case did
not repeat its command. A fresh harness reads the retained file. Store tests reject
valid foreign-tenant bindings and deleted Session ownership. Evidence is retained
under `~/.parsar/remediation/20260913/native-harness-relay/` on `zju_a100_2`.

This remains a transport prerequisite with synthetic credentials and zero model
calls, not public Environment admission, daemon dispatch or real-model Environment
acceptance. Only one independent harness connection per Environment is supported;
arbitrary interrupted-work replay, native crash restoration, TLS deployment and
the stock CLI's production-domain restriction remain open. Other harnesses retain
their own native placement and execution protocols.

## Native app-server placement prerequisite

The opt-in [real-provider fixture](../../services/agents-api/tests/native/README.md)
adds stock app-server execution to the accepted PostgreSQL registry/relay. It keeps
local harness history separate from a container-only executor workspace, exercises
real MiniMax shell/file use, and resumes the same native thread after a fresh
app-server. This is native placement evidence, not public API/daemon acceptance.

The pinned app-server loads registry configuration from its three
`CODEX_EXEC_SERVER_NOISE_*` startup variables. Its native Environment selector is
`remote`; that selector differs from the service Environment UUID used for registry
authorization. Supply executor-native cwd/roots in `thread/start.environments` and
`turn/start.environments`, while the app-server process stays in its local cwd.
Native resume does not restore these selections from history. Do not assume a
completed Turn proves remote readiness or tool execution.

Native `turn/interrupt` intentionally preserves unified_exec background processes.
The [upstream test](https://github.com/openai/codex/blob/3d2ee51ca2d5db578f328aa75e20aa22c0197c9a/codex-rs/core/tests/suite/unified_exec.rs#L2856)
asserts that behavior. To terminate a particular owned process, reuse experimental
`thread/backgroundTerminals/list` and `thread/backgroundTerminals/terminate`.
Correlate both item/process IDs with the original Turn's native events: listing
has no Turn ID, and termination can affect earlier Turns' retained processes.
Its acknowledgement does not wait for OS exit; observe the process and side effects
before claiming quiescence. Harness connection loss has a separate native detached
Session retention/cleanup window. These facts constrain the future cancellation
mapping; they do not independently establish hosted Agents API cancel semantics.
The daemon now supplies the observed native `turnId` (or the explicit empty startup
form) in its interrupt payload. Its applied receipt does not prove final output
settlement or process exit.

The native shell-policy default retains credential-like variables. The fixture
uses `inherit=core` and `ignore_default_excludes=false`; it separately characterizes
default-policy exposure without printing values. The Noise harness bearer is
non-inheritable, but that rule does not cover every executor launch credential.
No environment-variable policy isolates same-user process memory, `/proc` or files.
Scoped credentials, placement trust and long-Turn reconnect lifetime remain explicit
dispatch prerequisites. Public acceptance must verify those boundaries, readiness
and real API/daemon execution together.

## Private daemon adapter

The registered-daemon fixture extends placement through the authenticated gateway,
capability heartbeat and typed remote descriptor. The adapter consumes transient
connection credentials, verifies native readiness and selects the executor on first
and cold-resumed Turns. Local harness history remains separate from remote files.
See [the contributor boundary](../../CONTRIBUTING.md) and
[the real-provider fixture](../../services/agents-api/tests/native/README.md) for
supported native version, rejected combinations and acceptance commands.

The Codex adapter now separates preparation from prompt start using the same native
RPC resource. It retains initialized environment access without starting a thread
or model work, then transfers its fixed configuration and ownership once to the
normal Session. The existing Factory uses that path. Private daemon controls now
retain it through asynchronous preparation and one start, using a connection-owned
handle, bounded lifetime/capacity and separate gateway response correlation.
Preparation creates no Run subscription; only Start supplies the actual RunID.
The configured service Worker now uses this private primitive;
it does not expose public readiness. See the contributor guide for ownership, retry,
revision and cleanup rules.

Cancellation still uses the existing best-effort interrupt and harness release.
The fixture measures remote PID exit and stopped side effects independently;
native detached cleanup may delay that exit. Complete resource lifecycle and
complete public cancellation settlement remain separate from the initial text profile.


## Shared native filesystem prerequisite

The opt-in [shared-owner fixture](../../services/agents-api/tests/native/README.md#shared-native-filesystem-owner)
characterizes direct remote file operations alongside the upstream native model/tool
loop. It uses one injected `EnvironmentManager` and one authorized registry pair;
it does not open another harness connection or use stock host-only `fs/*` calls.
Native filesystem metadata supplies actual byte sizes, which the stock app-server
metadata response omits. Follow the [ownership boundary](../../CONTRIBUTING.md).

The fixture requires idle and active binary/file access, independent remote command
effects, and cold native history. Passing those observations does not establish
lossless delivery under saturation: the pinned embedding transport can drop
notifications without a `Lagged` event. Production event handling, process/credential
lifetime and daemon integration remain prerequisites. Public file create/list,
uploaded file references, workspace path semantics, pagination and installation
inventory remain unimplemented by this experiment.

## Pending input storage prerequisite

A private Store reservation can retain one ordered message batch without a Turn,
Items or Turn events. It shares request identity with direct input admission and
preserves the original five-minute database deadline across retries. Promotion
requires the leased writer and atomically creates history, settles the reservation
and claims its Turn as `in_progress`. Only the first non-replay receipts authorize
Start on the retained native preparation. Retries cannot reclaim execution. Startup
reconciliation settles a committed claim interrupted before Start, without replay.
Expiration, targeted cancellation and Session deletion retain their existing
pre-admission or claimed-Turn semantics.

The initial public idle-text profile uses this primitive. Its message-only scope
and single pending reservation are implementation limits, not claims about the
final protocol. Homogeneous results use the separate existing call-admission path;
active messages use existing input receipts in the same locked admission decision.
Mixed inputs remain required.
The Store reserves initial messages atomically with a new
Environment-bearing Session, preserving the creation cursor and retry identity.
Initial expiry emits a failed Session snapshot with a safe error and no Turn;
later expiry retains idle behavior. Historical reservation origins are not inferred.
The same storage rule covers internal hosted associations without enabling a
provider. Public ordinary and streamed creation reuse this transaction through
the Worker facade, with new-work ownership checks and prompt offline responses.
The Worker settles due reservations in bounded batches even without
devices or available execution slots, skipping contended Session locks and retaining
its current execution ownership. Restart does not reset stored deadlines. This
expiry creates no Turn; only expired initial reservations emit Session failure.
Exact hosted error wording, cancellation and crash behavior remain unverified. See the
[contributor boundary](../../CONTRIBUTING.md) for the prepared connection, expiry
and transaction rules.

The private Dispatcher now connects these prerequisites for an already bound
Session. It retains one capable daemon peer and preparation, checks the pending
deadline/ownership, then promotes and starts only fresh admission receipts. The
same delivery path journals events, applies later input/cancellation receipts and
persists terminal state and native continuation. A transient connection callback
keeps native registry code outside the execution core. Its credential owner spans
the complete Run; a pending-input deadline does not limit an admitted Turn.
Controlled database/gateway tests cover pre-ready settlement and pending-start
cancellation. The existing Worker selects pending reservations with a live tenant device when
configured with a connection resolver. Unbound Sessions select a capable device
and retain that binding; existing bindings are never moved. Preparation
through Run cleanup shares its four ordinary execution slots, with one active job
per Session and bounded, alternating cursor scans. Private pending selection runs
at most once per five seconds; failed preparation can retry without extending the
original deadline, while claimed/uncertain work is not replayed. The opt-in
real-provider Worker fixture verifies automatic discovery, remote commands, files,
cold continuation and reservation retries. The standalone service wires the resolver
when its daemon gateway and executor URL are configured. The same scheduling and
expiry path owns public initial reservations without a separate execution loop.
Caller keys resolve trusted project/subject identities, with persistent project
bindings verified before startup. New Sessions persist the typed creator and
require it for creation retries; historical unknown creators cannot be claimed.
Executor keys match the recorded project and typed creator. Complete lifecycle
conformance remains unverified.
The current daemon can acknowledge pending-start cancellation without a final
outcome; without an observed final Done, delivery records an unknown failure.
Preparation failure cannot discard a cancellation receipt already being awaited.
Complete cancellation output/Usage and native cleanup remain required work.

### Pending input activity and Session reads

The latest relevant reservation now owns a narrow pre-Turn activity projection.
Pending offline input emits `requires_action` with `environment_connection`;
connection arrival clears it to `idle` before native preparation admits a Turn.
Offline Sessions without waiting input request nothing. Newer or active Turns
supersede the reservation. Connection/reservation mutations and immutable activity
events commit together, including captured Usage; reads and SSE share that state.
Cancelling or expiring a non-initial reservation clears its action to `idle`,
without reviving an earlier failed Turn. Exact hosted settlement/error behavior and
initial-input asynchronous failure remain unverified; this policy does not claim
their compatibility.

With the validated executor origin configured, ordinary Session GET/list/metadata
and live SSE return `self_hosted` Sessions. Their safe
output contains the real Environment ID, unchanged configured `remote_url`,
workspace and capability directories. It excludes private configuration and does
not infer URLs from request headers. Fixed SDK/raw HTTP/live SSE acceptance uses
the returned URL and ID to start the real executor, then observes the existing
Worker's remote first/resumed model workflow. The earlier private-provisioning
fixture remains a separate lower-level regression; the public profile has its own
built-service creation/input acceptance. Environment retrieval uses the same durable
observations through the existing live-Session ownership join. It returns the seven
required fields and empty installed-resource arrays only for the closed supported
self-hosted configuration. No API-managed installation resource exists in that
profile; caller-prepared or model-created workspace files are not this inventory.
Unsupported installation fields/capabilities fail closed. This read does not require
execution/registry configuration or invoke native work. Populated metadata, file
operations beyond the current Files profile, populated hosted output and complete
Environment conformance remain separate work.

## Dependency-ordered implementation

1. **Executor interoperability.** Demonstrate the documented unmodified executor
   command with supported authentication, then native harness authorization,
   encrypted initialization, command output/exit and termination. Verify foreign
   tenant and wrong-purpose credential rejection. Do not grow a universal transport
   framework or change the protocol pin to get a passing probe.
2. **Durable ownership.** Create tenant/Session/Environment association atomically
   with Session creation and retry identity. Separate immutable configuration from
   mutable lifecycle/registration. Fence replaced registrations so stale disconnects
   cannot overwrite current observations. Implement safe resource reads and explicit
   event-state projection together with meaningful lifecycle behavior.
3. **Input and execution integration.** Represent `environment_connection` before
   waiting work starts. Connect/readiness gates precede claim; recheck ownership at
   dispatch. Pass a typed environment descriptor through the daemon boundary. Keep
   the harness's local cwd separate from the executor workspace; never locally
   create an executor-only path. Real model acceptance must cover remote file and
   command use, cancellation and continuation through API, daemon and native harness.
4. **Additional placement and resources.** Prove native co-location where useful;
   no MCP substitute is automatically equivalent to native tools. Add provider,
   template, confidential-input and file operations in independently accepted slices
   using maintained provider SDKs and existing storage/authorization infrastructure.

The [lifecycle guide](https://developers.openai.com/api/docs/guides/agents-api/environments/lifecycle)
requests compute through `environment_connection`, before Turn creation; connection
events only report observations. A waiting submission can continue when connection
arrives. The guide describes a five-minute wait and no guaranteed recovery of
pending input after a crash; a late connection does not replay timed-out work.
Session deletion and caller compute shutdown are separate operations. An idle
notification alone is insufficient evidence that compute can safely stop.

Exact hosted timing/errors, interrupted input recovery, executor replacement,
expiration, native cleanup and unsupported engine placements remain explicit
validation gaps. Preserve those gaps in the board and reassess its complete
priorities after each accepted slice. No placeholder resource, permissive SDK
parse or synthetic execution test establishes this roadmap as implemented.

### Durable executor credential prerequisite

The native registry authenticates connect-only executor keys against the target
Session's verified project partition and immutable typed creator. Keys may be
issued before Session creation or restricted to one live Environment. Their stable
management IDs, principal/restriction and digest survive restart. Explicit rotation
or revocation changes current authorization without restarting the registry.
Deleting one Session denies that target without revoking a key shared by other
matching Sessions. Legacy keys remain revoked with unknown principals; no identity
is inferred. See the [operator cutover](../../services/agents-api/README.md#native-executor-transport-prerequisite).

Existing sockets are checked on heartbeats; disconnection does not establish
process quiescence. Harness keys and five-minute connection grants have separate
purposes and lifetimes. This implements the executor-specific principal prerequisite;
it does not establish a general creator-only Session ACL, hosted key lifecycle/error
parity or stock-command support on arbitrary domains. Public idle-text admission
has separate built-service acceptance.
