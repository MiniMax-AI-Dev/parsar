# Environment contract and implementation path

This assessment covers the fixed [Python SDK contract](upstream.json). It is an
implementation plan, not an announcement of Environment support. Public execution
currently admits `environment.type=none` on the verified Codex and Claude SDK
profiles. No public Environment, template or file resource is implemented. An opt-in native
executor/harness authorization and native encrypted relay adapter exists; public
Environment execution remains unimplemented. See [current coverage](README.md#public-semantics).

The internal Store now owns a durable Environment association for newly created
`self_hosted` and `openai_hosted` snapshots, atomically with Session creation.
It derives configuration and tenant ownership from the Session; retries preserve
the existing identity. Scoped reads hide associations after Session deletion while
retaining the underlying record. This is a persistence primitive, with no public
admission, lifecycle transitions, readiness gating or native/provider integration.
Missing/`none` configurations and historical internal snapshots gain no backfill.


The native Codex registry uses exact-Environment executor digest bindings, the
existing execution owner and tenant-scoped Store reads. Registration and current
socket identity are process-local; the returned WebSocket capability expires for
new connections after five minutes. Restart invalidates registrations, causing the
native executor to register again. Replaced socket callbacks cannot clear a newer
connection. These observations do not change durable `pending` state or emit public
Environment readiness. Deleting the owning Session rejects new requests and closes
existing sockets on the next ownership heartbeat. A previous holder of a still-valid
executor credential can register again; permanent exclusion requires revocation.
Distinct harness credentials now obtain short-lived, key-bound connection grants.
The relay pairs one harness with the current executor socket and forwards native
binary frames unchanged. Either peer loss closes both physical connections and
invalidates grants; no queued frames or commands move to a successor. Refresh does
not disturb a healthy pair. See the [operator prerequisite](../../services/agents-api/README.md#native-executor-transport-prerequisite).

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
dispatch prerequisites. Public admission stays disabled until those boundaries,
readiness, lifecycle and real API/daemon execution are accepted together.

## Private daemon adapter

The registered-daemon fixture extends placement through the authenticated gateway,
capability heartbeat and typed remote descriptor. The adapter consumes transient
connection credentials, verifies native readiness and selects the executor on first
and cold-resumed Turns. Local harness history remains separate from remote files.
See [the contributor boundary](../../CONTRIBUTING.md) and
[the real-provider fixture](../../services/agents-api/tests/native/README.md) for
supported native version, rejected combinations and acceptance commands.

Cancellation still uses the existing best-effort interrupt and harness release.
The fixture measures remote PID exit and stopped side effects independently;
native detached cleanup may delay that exit. This slice does not implement public
`environment_connection`, admission/readiness-before-claim, credential renewal or
resource lifecycle. Public `self_hosted` remains disabled until those dependencies
are accepted together.

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

The native registry now authenticates exact-Environment executor credentials from
the execution database. Operator issuance returns a random secret once; explicit
rotation/revocation replaces or invalidates its digest without restarting the
registry. Owning Session deletion also removes authority. Existing sockets are
checked on heartbeats; disconnection does not establish process quiescence.
Harness keys and five-minute connection grants retain separate lifetimes. See the
[operator transition](../../services/agents-api/README.md#native-executor-transport-prerequisite).
This does not implement public caller/user/service-account identity equivalence,
public `self_hosted` admission or the stock command on arbitrary production domains.
