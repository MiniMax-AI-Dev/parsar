# Agents API contract

The external reference is [openai-python beta/agents](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents),
pinned in `upstream.json`. Its resource methods, corresponding types, pagination
and streaming helpers define the compatibility target. This directory records
the boundary; it does not imply that every upstream feature is implemented.

Parsar owns product Agents and Teams. This service owns upstream execution
resources, including reusable Agents and protocol subagents. The OpenAI Agents
Python **SDK** is a separate future dependency for business Team orchestration in
Parsar, not the HTTP contract. Design rules live in
[CONTRIBUTING.md](../../CONTRIBUTING.md#design-and-compatibility-requirements).

## Implementation direction

Keep the independent service, authentication, PostgreSQL/sqlc persistence,
transactional admission and official-client test harness. Replace the parts that
let legacy daemon representations define execution semantics. Starting over is
permitted where a replacement is smaller and clearer; neither a wholesale rewrite
nor compatibility with the old private implementation is a goal.

Concentrate native configuration, structured input/output and Item translation
in an execution adapter. The application core owns execution state and persistence;
engine-specific shapes stay at the adapter boundary. Codex uses its native
app-server; Claude uses the maintained Agent SDK. Reuse native protocols and SDKs
for further harnesses rather than adding another model/tool loop.
The [harness contract and parity baseline](harnesses.md) describes equal-engine
registration, qualification and shared acceptance.
Verify configuration against actual execution: response defaults must not merely
describe values the adapter never applied.

The private native registry persists fenced connection observations and pinned
Environment-event snapshots through the existing execution owner. Session reads
and live SSE also expose safe `self_hosted` output and reservation-owned connection
actions. Public self-hosted creation accepts initial text or empty Codex Sessions;
initial input reserves work while returning the connection target promptly.
Later idle text submissions wait for preparation/admission. Cancellation-only events
reuse durable admission without creating work or retargeting retries; pending
pre-Turn input still blocks new cancellation. HTTP acceptance does not establish
native completion or process quiescence. Environment retrieval
exposes durable status and safe empty installation metadata for that profile.
Non-deferred functions and homogeneous result-only batches reuse the existing
callback/application path, with explicit call identity and no new Turn on results.
These callbacks are not installed Environment resources.
Message-only batches append to an active Turn under the same Session lock that
reserves idle work; retries retain their original target through completion and later work.
Populated installation metadata, mixed input and full lifecycle conformance remain
unimplemented; see the [Environment scope](environments.md).
Initial messages commit with creation and a connection action; an initial deadline
failure is queryable before a Turn exists. Ordinary and streamed creation share this path.

The three-harness Docker V1 MVP is accepted: Codex, Claude Code and MiniMax Code
share the execution/workspace contract, with independent Core/database deployment,
Files/Artifacts, cancellation and owned-history continuation. Optional features
still differ. See the [accepted scope and evidence](#accepted-milestone-and-evidence).
The same three harnesses also passed separate real E2B V1 qualification in PR #705;
see the [E2B operator guide](../../services/agents-api/deploy/e2b/README.md).
Select further work only within current user authorization. Parsar cutover and
business Team orchestration are separate from protocol coverage.

## Upstream resource inventory

This inventory is based on the pinned Python source, not our generated OpenAPI.
It contains 42 distinct HTTP operations in 15 resource classes, excluding async
duplicates, overloads and client-side helpers. There are 36 handler entries; the six
Subagent read operations remain missing. The separate
general `/v1/files` source-file API is outside this 42-operation count.

An implemented route is not complete semantic compatibility. **Accepted** below
means a recorded workflow passed under a specific profile; **partial** means some
variants work; **missing** means no implementation; **unverified** means behavior
has not been shown to match upstream. Do not convert the route count into a
compatibility percentage or treat a Docker result as E2B qualification.

Paths below are SDK resource paths beneath `client.beta.agents`. Method names use
the Python SDK. Vault HTTP paths start at `/vaults`, not `/agents/vaults`.

| Resource | Upstream operations | Current coverage |
| --- | --- | --- |
| Root reusable Agents | create, retrieve, update, list, delete | Partial create/retrieve/update/list/delete and Session references; configuration/error gaps remain |
| sessions | create, retrieve, update, list, delete | Create (ordinary/live), retrieve, list with root-Agent filter, metadata-only update, public deletion with owned Docker/E2B cleanup; general physical cleanup and exact hosted semantics remain open |
| sessions.events | create, stream | Text/cancel/function-result admission and live events; function-action state snapshots supported |
| sessions.turns | retrieve, list | Implemented reads; lifecycle conformance still partial |
| sessions.items | list | Partial Item variants |
| sessions.artifacts | retrieve, list, delete, content | Shared output capture and immutable stored reads/deletion on the accepted three-harness Docker/E2B profiles, including retained downloads after Runtime loss; exact upstream defaults/errors, unchanged-file republishing and cancellation-edge parity remain unverified |
| sessions.subagents | retrieve, list | Missing |
| sessions.subagents.items | list | Missing |
| sessions.subagents.turns | retrieve, list | Missing |
| sessions.subagents.turns.items | list | Missing |
| environments | retrieve | Supported Codex self-hosted and three-harness Docker/E2B hosted profiles: durable status and safe initial-file metadata; other installation inventory and full lifecycle parity remain gaps |
| environments.files | create, list | [Bounded live listing and inline/source-file creation](environment-files.md) on qualified Docker/E2B workspaces; Codex self-hosted listing is a separate supported path. Full listing, overwrite and error semantics remain partial |
| environments.templates | create, retrieve, update, list, delete | [Reusable network, files, env/setup/packages, inline Skills and Session snapshots](environment-templates.md); other initialization and full semantics remain gaps |
| vaults | create, retrieve, list, delete | Create/retrieve/list/delete with independent tenant persistence, stored status filtering, atomic Credential cascade and frozen Session attachments; archive semantics and full hosted lifecycle parity remain missing |
| vaults.credentials | create, retrieve, update, list, delete | Static-bearer create/retrieve/list/token replacement/deletion with scoped encrypted storage; Session attachment and exact-URL HTTPS MCP binding; OAuth, archive semantics and full hosted lifecycle parity remain missing |

For each resource, verify the referenced request/response unions and observable
behavior, not just the route. Non-text initial input, configuration
options, text/image content, function results, environment variants, full Item/SSE
variants, defaults, field omission/nullability and errors need their own cases.
Use strict official-client tests plus raw HTTP assertions; SDKs can accept extra
fields and cannot prove that reported configuration matches the running engine.
Where SDK types or public documentation do not establish behavior, record the
uncertainty and obtain upstream evidence before marking it conformant. Temporary
unsupported errors are implementation gaps, never evidence of full compatibility.

## Accepted milestone and evidence

The three-harness Docker V1 milestone was accepted on 2026-09-20 after
[PR #703](https://github.com/MiniMax-AI-Dev/parsar/pull/703). A fresh source-free Core
package and main-built daemon passed fixed Python SDK 3.13.0/raw HTTP/real Kimi K3
regressions for Codex, Claude Code and MiniMax Code. The profiles use independent
execution databases and no Parsar services or product database.

These final regressions supplement, rather than repeat, every earlier check.
Codex's full 26-check deployment evidence, Claude's hosted/Artifact/crash/security
evidence, and MiniMax's real MiniMax Artifact and Core/Runtime SIGKILL evidence
retain their exact tested scope. The full gate, focused race checks and fresh
Astra high review passed for the candidate; no additional live run is claimed by
this documentation update. Evidence on `zju_a100_2`:

- `~/.parsar/remediation/20260920/three-harness-mvp/REPORT.md` and
  `acceptance-results.json`: final baseline, runs, reused evidence and cleanup.
- `~/.parsar/remediation/20260919/docker-mvp/` and `claude-v1/`:
  preceding deployment and safety acceptance.
- [MiniMax workspace qualification](mcode-workspace-v1.md) and
  `~/.parsar/remediation/20260919/mcode-workspace/`: native isolation and recovery.

Qualification is Linux amd64 Docker V1, not arbitrary host isolation, production
HA, E2B, or Anthropic-model acceptance for Claude Code. No exactly-once guarantee
is made for future model choices: the recorded Kimi continuation limitation is a
new model-issued command after a recovery prompt, not automatic API replay.

### E2B V1 qualification

PR #705 (`9cd1c46c7fab6eeef8cb35ce71f1ea0ca2cf8bc1`) separately qualified
Codex, Claude Code and MiniMax Code with actual E2B and real Kimi/MiniMax APIs.
Fixed SDK/raw HTTP acceptance covered independent Core/database deployment,
Files/Artifacts, auth/tenant and native credential/history isolation, cancellation,
Core/Runtime crashes, exact-history continuation without replay and native network
restrictions. All three runs completed cleanup without fallback. The five Provider
operations passed real lifecycle/race acceptance; `make check` and fresh independent
Astra high review passed. The merged tree matches the accepted candidate.

Evidence: `~/.parsar/remediation/20260920/e2b-runtime-v1/` on `zju_a100_2`, including
`acceptance-results.json`, `provider-real-final.log`, `make-check.log`,
`blind-review.md` and exact image/template build pins. This uses the existing
colocated Runtime contract, with no public resource or protocol expansion.
The qualified Linux amd64 templates require a reachable HTTPS/WSS Core and a
minimum two-hour renewable E2B lease. Expiry destroys volatile workspace/history;
unknown effects cannot authorize recreation or replay. Pools, migration and
user-managed enrollment remain outside this qualification.

### Remaining protocol work

| Area | Missing or unverified scope |
| --- | --- |
| Subagents / multi_agent | Six public child read operations, enabled execution, child lifecycle/interactions and full recovery; deferred outside the MVP |
| Environment Templates | Skills references, Plugins, capability directories, restricted network, installation overrides/null network and exact hosted errors; CRUD/list, files, env/setup/system/npm/Python, inline Skills and Session references are supported |
| Input and configuration | Non-text initial input, broader content/configuration unions, structured output and reasoning/verbosity combinations |
| Tools and interactions | Deferred functions, other tool types, effective tool-set enforcement and result/cancel publication ordering; MiniMax public functions/MCP remain unsupported |
| Vault and Credentials | OAuth/refresh, archive semantics, revocation/concurrent mutation and exact hosted selection/error behavior; static bearer CRUD/token replacement is already present |
| Existing resources | Full Item/SSE/Usage variants, omitted/null/default/error semantics, pagination and overlapping lifecycle behavior beyond recorded cases |

An implementation gap and an unknown upstream behavior require different follow-up
work. Retain both explicitly; a restrictive local policy or successful SDK parse
cannot establish upstream equivalence. The Feishu board owns live task selection,
including further deployment qualification; this inventory describes merged behavior.

## Public semantics

- Credentials use `POST /vaults/{vault_id}/credentials` and
  `GET /vaults/{vault_id}/credentials/{credential_id}`. The initial profile accepts
  only `static_bearer` with required string token and HTTPS destination, plus a
  required name trimmed to 1–256 UTF-8 bytes. Tokens remain opaque, including empty
  strings; exact hosted token validation is unverified. The local URL profile
  excludes userinfo/fragments and preserves queries without normalization or network
  contact. Public metadata contains identity, owning Vault, name, timestamps and
  auth type/destination; it never returns tokens or ciphertext and can be read
  without the encryption key. Missing encryption configuration locally rejects
  creation/replacement with 503. Attached Sessions can use static credentials for
  exact-URL HTTPS MCP; OAuth, storage-key rotation, archive behavior and key scopes remain gaps. See the [credential storage guide](../../services/agents-api/credentials.md)
  for encryption and operational limits; this does not establish complete Credential
  or hosted error/retry compatibility.
- `GET /vaults/{vault_id}/credentials` lists only safe metadata, with parent and
  cursor ownership checked within the authenticated project and requested Vault.
  It uses the same paging/filter grammar as Vault listing below. Credential status
  is stored separately from Vault status, defaults active, and never appears in
  the public response. Both active and archived Credentials are included by default;
  synthetic archived fixtures establish read/filter behavior, not archive lifecycle.
  No token/ciphertext column, decryption, execution or encryption key is needed.
  An inaccessible parent is not returned as an authorized empty collection. Existing
  create/retrieve/token replacement and dispatch rules are unchanged; exact hosted
  errors and pagination under concurrent mutation remain unverified.
- `POST /vaults/{vault_id}/credentials/{credential_id}` replaces a static token using
  only required `auth.type=static_bearer` and string `auth.token`. It preserves opaque
  strings, rejects missing/null/type/extra-field mutations and returns safe metadata.
  Ciphertext/update time change atomically within the same tenant/Vault/ID/type/URL;
  name, destination, identity, creation time and Session bindings are unchanged. No
  old-token decryption or MCP call occurs. Subsequent dispatch reads use the committed
  replacement; already-resolved requests may retain the old token. OAuth replacement,
  storage-key rotation, hot reload/revocation and exact hosted concurrent-update,
  timestamp and retry semantics remain gaps.
- `DELETE /vaults/{vault_id}/credentials/{credential_id}` returns only `id`,
  `deleted: true` and `object: vault.credential.deleted`. It removes one owned row
  and its ciphertext without an encryption key. Local retrieval/update/repeated
  deletion then return 404; lists omit it. Frozen Session choices and history remain
  intact, while subsequent secret lookups fail without credential reselection or
  anonymous fallback. Already-resolved tokens and running Sessions are not revoked.
  Archive relationships, exact hosted post-delete visibility and repeat/error
  semantics are unverified; physical storage erasure is not established.
- Vaults use `POST /vaults`, `GET /vaults` and `GET /vaults/{vault_id}` with the same project/tenant
  authentication and Beta header as other resources. The response contains only
  `id`, `object: vault`, `created_at`, `name` and `metadata`. Omitted name stays null;
  explicit null is rejected. Supplied strings are trimmed and must contain 1–256
  UTF-8 bytes. Omitted/null metadata becomes `{}` and values must be strings.
  Session-specific metadata pair/character limits do not apply. The existing
  64 KiB encoded metadata and 1 MiB HTTP body bounds are local implementation
  limits. Creation does not start execution. Retrieval maps missing, malformed and
  foreign IDs to the same local not-found response. Exact hosted error/retry semantics,
  restricted-key scopes and archive lifecycle remain unverified or unimplemented;
  this is not complete Vault compatibility. Listing accepts `after`, creation order
  (default `desc`), a default limit of 20 clamped to 1–100, and scalar or SDK bracket-array
  `status` filters. Both `active` and `archived` are included by default. The private
  classification is stored, never returned; existing/new Vaults default active.
  Synthetic archived fixtures prove read/filter behavior only. No public archive
  writer or delete-to-archive mapping is implemented. Equal creation times use ID
  order locally; repeated scalars and mixed status encodings are rejected. Exact
  hosted query errors and pagination over changing data remain unverified.
- `DELETE /vaults/{vault_id}` returns `id`, `deleted: true` and `object: vault.deleted`
  after project-scoped parent removal and atomic cascade of all stored Credentials.
  It needs no encryption key or execution connection. Local parent/child reads,
  repeated deletion and new references return 404; lists omit the removed resources.
  Existing Session snapshots and recorded retries retain their IDs and selections.
  Subsequent secret lookup fails without reselection; already-dispatched tokens are
  not withdrawn. Archive relationships, exact hosted visibility/concurrent errors,
  provider revocation and physical erasure remain separate gaps.
- Reusable Agents use `POST /agents` and `GET /agents/{agent_id}`. Keep their own
  identity, timestamps and metadata separate from Session effective configuration.
  On creation, omitted/null name and instructions resolve to null, metadata to `{}`, tools to
  `[]`, text to ordinary/medium, and multi-agent settings to disabled/null. Enabled
  multi-agent settings default to six concurrent subagents. Function defer-loading
  defaults to false and programmatic tool calling to true. Saving these values
  does not itself admit a native execution. Session references are admitted separately.
- `POST /agents/{agent_id}` updates only supplied fields. Omitted fields remain
  unchanged; metadata replaces all pairs and null/empty clears it. Name/instructions
  null clears them. Concurrent updates preserve unrelated fields. Existing Session
  snapshots and their recorded creation-retry identity remain unchanged; new Sessions
  resolve the latest saved configuration. No-field updates leave timestamps unchanged.
  Nested fields currently replace whole values and null uses the saved defaults;
  hosted nested/null behavior, no-op timestamp policy and exact errors remain
  unverified. This operation shares the existing saved-configuration coverage gaps.
- `DELETE /agents/sessions/{session_id}` returns the canonical `id`,
  `object=agent.session.deleted` and `deleted=true` after durable public removal.
  Session/Turn/Items reads, live streams, metadata updates and new input exclude
  the resource. Queued work is cancelled; active work receives the existing
  asynchronous cancellation request while internal finalization remains available.
  Existing streams close on observing removal without an invented deletion event.
  Creation keys remain reserved (local 409); missing/repeated deletion locally
  returns 404. Qualified managed Docker/E2B deletion also reclaims its owned Runtime;
  broader physical SQL/native history cleanup, immediate native quiescence and
  exact hosted error/retry/overlapping-stream semantics remain unverified or
  unimplemented. Shared devices, saved Agents and other Sessions are independent.
- `DELETE /agents/{agent_id}` removes the tenant-owned saved configuration and
  returns `id`, `object=agent.deleted`, and `deleted=true`. Existing Sessions and
  history are retained; recorded creation retries recover their frozen snapshot,
  while new references to the source fail. Local missing/repeated deletion returns
  404. Exact hosted errors and overlapping creation/deletion ordering are unverified.
- `GET /agents` lists tenant-owned reusable resources with `after`, `limit` and
  `order` (default `desc`). It uses creation-time/ID keysets and the same resource
  mapping as retrieval. Positive int64 limits are accepted; pages contain up to
  100 resources, with `has_more` and the final resource ID guiding continuation.
  The local default is 20. The list envelope includes `object`, `data`, `has_more`,
  `first_id` and `last_id`; empty pages use null IDs. The pinned SDK omits null
  limits and empty cursors. Exact upstream default/cap, empty-envelope nullability
  and error taxonomy remain unverified; SDK auto-pagination does not prove them.
- Saved Agent model-default reasoning resolution remains missing: an omitted effort
  stays unresolved rather than being populated from a guessed model default. An
  explicit effort/summary is retained. Omitted/null service tier currently follows
  the service's `auto` policy; complete upstream-default/error/retry conformance is
  unverified. HTTP MCP with explicit `service` origin and
  boolean `required` (default false) supports saved configuration and Codex `none` execution,
  plus `self_hosted` execution behind explicit combination capabilities. Remote
  static Bearer authentication additionally requires `mcp_http_remote_bearer_auth`;
  it keeps the secret in the trusted service native process. Required initialization
  additionally needs `mcp_http_required` on the pinned native profile. Native root
  thread creation/cold resume must initialize required servers before a native
  Turn starts; failure cannot silently replace retained history. Public acceptance
  or queued work does not prove native readiness. Hosted creation timing/error
  parity and continuing server health remain unverified.
  The saved HTTP transport includes `headers:{}`; effective Session transport omits
  headers. Omitted/null `allowed_tools` is unrestricted; `[]` denies all tools.
  Session `vault_ids` attaches tenant-owned Vaults. Explicit `credential_id` must
  belong to an attached Vault and match the exact HTTPS URL; omission/null selects
  one matching static credential, zero stays anonymous and ambiguity fails. Private
  immutable selections do not populate the public credential field. Scope is
  rechecked before dispatch-only decryption; authenticated execution requires the
  separate bearer capability and never downgrades on failure. Exact URL/selection
  timing, implicit response population and hosted errors remain local or unverified.
  Other MCP variants and web-search remain gaps, not changes to the pinned target
  or claims of complete resource coverage.

- Use `/agents/sessions` beneath the configured API base URL, bearer authentication
  and `OpenAI-Beta: agents=v1`. Do not introduce a competing `/sessions` surface.
- Session creation takes an environment and inline agent configuration or a saved
  agent reference. The saved ID and effective configuration are copied into an
  immutable Session snapshot. Omitted fields inherit; supplied objects and arrays
  replace the entire field ([configuration guide](https://developers.openai.com/api/docs/guides/agents-api/configuration)).
  Tools null clears the list as specified by pinned `session_create_params.py`.
  Saved metadata never becomes Session metadata. A model name is not a daemon engine name.
  Current admission requires disabled multi-agent, implicit reasoning, tier `auto`,
  ordinary text and non-deferred functions. Unsupported saved settings fail before
  Session persistence, unless replaced by supported overrides. Other native options
  remain implementation gaps, not excluded protocol variants.
  Fixed SDK/raw HTTP checks cover inherited/overridden configuration, tenant ownership,
  source preservation, independent Session snapshots, retries and service restart.
  New saved-reference Sessions record caller intent before source lookup. Matching
  creation retries recover their accepted snapshot even after source update/deletion;
  changed overrides conflict. Retries also require the original typed creator.
  Known creators without recorded request intent retain resolved-hash behavior;
  records without creator identity reject retries. Neither identity is backfilled. These local
  retry rules are not verified hosted semantics.
  In the pinned `session_create_params.py`, `stream` defaults to false and neither
  `stream` nor `agent_id` permits null. Metadata omission/null defaults to an empty
  map; individual values must be strings, including valid empty strings. Validate
  these distinctions before persistence rather than coercing null to Go zero values.
- `POST /agents/sessions/{id}` updates metadata only: omission preserves it,
  null or `{}` clears it, and an object replaces all pairs. Apply the same string
  and character limits as creation. Preserve execution state, effective configuration
  and the original creation retry identity. Fixed SDK/raw HTTP checks cover these
  distinctions, tenant isolation, active Session reads and restart persistence.
- `GET /agents/sessions` accepts `after`, `limit` (1..100, default 20), `order`
  (default `desc`) and optional `agent_id`. The filter matches the immutable root
  Agent ID, including inline IDs and Sessions whose saved source was changed or
  deleted. Filter before pagination within the authenticated tenant; no source
  lookup is required. Omission lists all Agents. Empty filters, same-tenant cursors
  outside the filter and exact hosted errors/defaults remain unverified.
- `AgentSession` includes the effective agent/environment, Unix-second timestamps,
  `object: agent.session`, metadata, required actions, status, usage and vault IDs.
  A Session remains reusable after its current Turn completes.
- Input, cancellation and function results are submitted through session events.
  Turns are queried through `/agents/sessions/{id}/turns`; do not invent turn-create
  endpoints. Event submissions support the `Idempotency-Key` header.
- Per the [official Session guide](https://developers.openai.com/api/docs/guides/agents-api/sessions),
  input steers an active Turn and starts a new Turn when idle. Streams are live-only;
  recover missed work through persisted Session/Turn/Items queries, not assumed SSE
  replay. Internal input ordering is not a public event-stream cursor.
- List operations use the upstream `after`, `limit`, `order` and resource-specific
  filters. Stream events preserve the upstream discriminators and payload shapes.
- The upstream self-hosted environment includes an exec-server `remote_url`.
  A Parsar daemon socket is not automatically compatible with that transport.
  Provider adaptation must be explicit and verified before advertising support.
- Environment retrieval returns `object: agent.environment`, its ID/type, durable
  resource status and required non-null `files`, `plugins` and `skills` arrays.
  Hosted initial files report safe frozen metadata; empty arrays do not
  describe native discovery or workspace files created by commands. Unknown
  installation configurations are rejected, not reported as empty. Reads use the
  owning live Session's project partition and do not require execution setup.
  Remaining unsupported installation configuration, full hosted lifecycle and
  exact hosted error semantics remain gaps.

[Environment Templates](environment-templates.md) provide tenant-owned CRUD/list
and immutable Session resolution through the same hosted initialization. They do not
select an E2B image or make unsupported initialization executable.

## Delivery and verification

| Capability | Current state |
| --- | --- |
| Independent deployment | Source-free Core package and separate execution PostgreSQL ownership; managed Docker/E2B Runtime co-locates daemon, selected harness and workspace; no Parsar dependency |
| Saved Agents and Sessions | Saved Agent routes, immutable inline/referenced Session configuration, metadata updates, root-Agent filtering and scoped cursor pagination |
| Public execution | Initial/later text, active input and cancellation through Codex, Claude Code or MiniMax Code; Codex/Claude additionally support qualified public functions; see profile limits below |
| Pending function actions | Persisted calls/results/application receipts, `required_actions`, Session `requires_action`, Turn `waiting`, and live state snapshots; other interactions remain incomplete |
| Public recovery and SSE | Persisted Turn/Items queries and partial Usage; live lifecycle/Item/text events, creation streaming and the official one-Turn tool-handler helper |
| Execution ownership | Immutable Session engine/device, durable input receipts and database writer fencing; uncertain claimed work fails on restart, without blind replay |
| Files and Artifacts | Bounded Environment listing and inline/file_id copies into qualified V1 workspaces; source-file lifecycle and immutable output capture/download/deletion; [Files limits](environment-files.md), [source limits](source-files.md) |
| Clients | Fixed Python SDK 3.13.0 and official Go SDK v3.61.0; raw HTTP and real provider acceptance supplement controlled tests |
| Release and product | Registry publication and Parsar cutover remain open; basic Docker/E2B provisioning is operator opt-in; business Team orchestration is deferred |

### Public engine profiles

`AGENTS_API_ENGINE` supplies the default for new Sessions. The optional
[Core harness extension](harness-selection.md) explicitly selects an enabled engine;
existing Sessions retain their immutable choice. Model identity is independent.
All three profiles require disabled `multi_agent`, implicit reasoning, service tier
`auto` and ordinary text output. Optional tools/configuration are qualified per
operation and placement; native support is not public admission by itself.

| Engine | Qualified placements and limits |
| --- | --- |
| `codex` (default) | `none`, the bounded official `self_hosted` path and Docker/E2B `openai_hosted`; public functions with ordered text/image results; service-origin HTTP MCP on `none`/`self_hosted`, not hosted; supported verbosity follows the native policy below |
| `claude_sdk` | `none` and Docker/E2B `openai_hosted`; medium verbosity, object-root function schemas and text-only function results; anonymous/static-bearer HTTP MCP with either required value on `none`; hosted HTTP MCP remains unsupported |
| `mcode` | `none` text and Docker/E2B `openai_hosted` workspace execution; medium verbosity; public functions/MCP, image input and complete public usage breakdown remain unsupported |

All three hosted profiles reuse the [Docker](environments.md#basic-public-docker-hosted-profile)
or [E2B](environments.md#basic-public-e2b-hosted-profile) provider lifecycle,
workspace Files/Artifacts, cancellation and recovery queries, with engine-specific
native isolation. Configuration and immutable Runtime images/templates require explicit
operator setup: [Codex](../../services/agents-api/deploy/codex/README.md),
[Claude Code](../../services/agents-api/deploy/claude/README.md), and
[MiniMax Code](../../services/agents-api/deploy/mcode/README.md). The
[E2B guide](../../services/agents-api/deploy/e2b/README.md) packages those qualified
images as pinned templates.
The shared initialization path supports env/setup and system/npm/Python packages;
see the [evidence and limits](environment-templates.md#verification). Remaining
unsupported startup installations, restricted domains and hosted public HTTP MCP
remain outside these accepted profiles. MiniMax's private MCP tool bridge
is internal transport, not public MCP support.

The [Codex self-hosted profile](environments.md) remains distinct from managed
Docker/E2B and from future user-managed Runtime enrollment. Product `claude_code`
is likewise a separate integration from the API's `claude_sdk` engine key.
Unsupported configurations fail before Session creation; unsupported results fail
before a batch write. Native capability claims cannot replace service profile
qualification, tenant authority or exact binding checks. An existing Session
never silently changes engine/device. See the
[HTTP MCP limits](../../services/agents-api/README.md#http-mcp-execution).

The Store's internal DTO is not the upstream response model. The API layer must
validate and resolve the upstream schema before persistence, and report only
supported options. For example, upstream metadata is limited to 16 pairs with
64-character keys and 512-character values; a storage byte limit is not a
replacement for that public validation.

Use the pinned official Python client against the actual service, with response
validation enabled, for supported Session/Turn/Items operations, pagination, streaming,
errors, idempotency and tenant isolation. A client import or permissive parsing
alone is not evidence of compatibility. Unsupported capabilities must be explicit
errors, not successful placeholder resources. Add any provider or engine-specific
extension separately from upstream fields and document it here when implemented.

`openapi.yaml` is our generated supported surface; it is not the full upstream
specification. The shared Go wire types are in `v1`. Physical Session cleanup, non-text
message input, structured output execution, broader options/tools, remaining Vault lifecycle,
Subagents and environment/provider resources remain incomplete. Reject unsupported
requests explicitly; persisted saved configuration is not execution admission.

### Native subagent control

The pinned `types/beta/multi_agent_config.py` defines `enabled=false` as disabling
subagent tools. The dispatcher enforces that effective value with a typed daemon
policy and capability admission; the Codex adapter applies native feature controls
on fresh and resumed Turns. Operator feature preferences cannot re-enable them.
Controlled model-boundary tests check absence of direct/deferred subagent tools
while the official function workflow continues to run.

Disabled `multi_agent` input is admitted. Enabled multi-agent execution and public
Subagent resources are still unsupported. Do not infer that `Agent.tools` is the
complete native tool registry: environment and subagent tools have separate
configuration. The upstream behavior of internal Goal, Skills and user-input
utilities needs further evidence; their presence alone is not proof of a mismatch.

A private typed discovery path projects verified native child identities through
the leased execution journal. It freezes service identity, Session ownership,
native parent/creation and first-observation provenance for internal scoped reads.
It does not enable the public resources above or infer lifecycle from idle/unload.
Bounded discovery and delivery rules are documented in
[the contributor guide](../../CONTRIBUTING.md#current-implementation).

### Turn recovery reads

`GET /v1/agents/sessions/{session_id}/turns` and retrieval by `turn_id`
return persisted Turn states using the pinned official client contract. Lists
support `after`, `limit` (1..100, default 20), and `order` (default `desc`).
The cursor is a Turn ID in the same tenant and Session. Failed turns expose a
generic `internal_error`, never raw engine diagnostics. `usage` exposes the latest persisted complete token breakdown, including cached input
and reasoning output. Missing measurements remain null; Session usage sums recorded
Turn measurements as best-effort usage, without estimating missing history. Session runtime state derives from the latest Turn.

### Item recovery reads

`GET /v1/agents/sessions/{session_id}/items` supports the same list controls,
with a stable Item ID cursor and first-observation ordering. Messages preserve
text, phase and completion snapshots. Commands preserve reported output, exit
code, duration and working directory. MCP calls preserve server/tool identity,
arguments and structured results/errors. Dynamic functions have linked call and
result Items. Native file changes appear as `apply_patch` function calls with
reported changes as arguments; no result is invented when the engine reports none.
Web search exposes its supported action fields.

Terminal Turns make unfinished Items `incomplete`; a failed tool does not imply
that the Turn failed. Native start/completion snapshots and Codex command-output fragments are
available when the daemon emits them; other
tool-output deltas remain unsupported. Tool output is visible to the Session's
authenticated tenant and may include the command's or tool's own diagnostic text.

Reads use the durable index without reconstructing native journals. Existing
indexed history is preserved. Migration 15 requires old unindexed archives to be
prepared by release `906069e` before upgrade; see the
[upgrade procedure](../../services/agents-api/README.md#upgrading-archived-item-history).
The retired archive format could not recover unrecorded message boundaries or
outcomes; those limitations remain in already indexed historical Items.
Unsupported native variants, reasoning, subagent Items and Items mutation
are not covered. Public submission supports text messages, cancellation and function results.

Legacy Done frames alone do not complete assistant Items. Aggregate answer text
is confirmed by successful Turn termination; failed Turns retain observed deltas
instead of treating adapter diagnostics as assistant output.

Pagination orders by first-observation timestamp, then the Item's immutable
Session position and public ID. New Items retain observation order even when
timestamps match. The index also stores a zero-based output index per Turn for
streaming; inputs do not consume it. Updates and retries do not move Items
or change output indexes. Existing indexed history retains its pre-upgrade
deterministic order rather than guessing an unavailable original source order.

### No-environment execution

The dispatcher executes public `environment.type=none` on an authenticated,
bound host advertising `environment_none`. Codex uses `CODEX_EXEC_SERVER_URL=none`
and verifies native environment state before starting/resuming. Claude SDK uses
its restrictive profile with no built-in tools and only declared function callbacks.
A missing capability or unsupported native method fails rather than silently
allocating a local execution environment. Native state still lives on the host;
function callbacks may access their own resources. This is not filesystem isolation.
Private `daemon` snapshots and the self-hosted registry/Noise transport are
distinct from this mode.

The native reference is Codex `rust-v0.153.4`, commit
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`, especially
`codex-rs/exec-server/src/environment_provider.rs`. The self-hosted registry
requires executor registration, harness authorization and encrypted relay;
a daemon WebSocket URL is not that protocol.
The [Environment assessment](environments.md) records all environment/template/file
operations, ownership, native authentication gaps and the implementation sequence.

### Public execution admission

`POST /v1/agents/sessions/{session_id}/events` accepts `agent.session.input.message`
with user `input_text` content, `agent.session.input.cancel` and
`agent.session.input.tool_result`. Successful atomic
admission returns 204, as consumed by the official `events.create` method. A retry
key identifies the entire ordered request; conflict does not partially admit it.
Messages start queued work or steer the active Turn. Individual input messages
remain distinct Items even when their text shares one native prompt.

Enable the standalone daemon gateway to run the worker; without it, admission
returns 503. The worker selects capable same-tenant engine hosts, binds each Session
once, and runs at most four Turns concurrently. Queued cancellation needs no live
engine. Active cancellation waits for a native receipt; terminal completion can
win that race. Query Turn/Items to recover results after a stream interruption.

The worker takes a database advisory lease; a second worker cannot start on the
same database. All execution writes use that lease connection and stop after loss
of ownership. Startup marks previously claimed Turns failed without replaying them
and retains queued work. Database fencing does not stop already queued native
commands, recover missing daemon frames or guarantee exactly-once external effects.
Session status reflects the latest persisted Turn; usage reports recorded measurements.

Native verification uses `PARSAR_NATIVE_DAEMON_BIN`, `PARSAR_NATIVE_PROOF_DIR` under
`~/.parsar/`, and `PARSAR_OFFICIAL_SDK_PYTHON` pointing to the pinned SDK environment.
The Store native integration test runs `tests/official_execution.py` against a real
HTTP handler, PostgreSQL, daemon and Codex with a synthetic model provider.

Token measurements use the pinned SDK's `TokenUsage` fields. The optional daemon
`usage.tokens` supplies complete per-Turn counters; journal and terminal writes
replace that Turn's snapshot atomically. Repeated snapshots do not increase totals.
Unknown historical breakdowns are not backfilled, and a Session total includes only
recorded measurements. Costs and prices are outside this execution contract.

### Live events

`GET /v1/agents/sessions/{session_id}/events` implements the official live-only
stream. Open it before submitting input. Session in-progress/idle/failed and Turn
created/in-progress/completed/failed/cancelled events carry transition snapshots.
Supported Items emit added/done events; assistant text emits content-part and
text-delta/done events. Inputs, including function results, have no output index. Function results emit
`item.added` and remain queryable; `item.done` only carries agent output. Public
function-result output/error retain the saved submission and field presence;
native error-to-text translation does not rewrite those fields. Completed text replaces
accumulated deltas; cancelled unfinished Items retain their partial content and
`incomplete` status. Codex command-output fragments use the pinned
`agent.output.command_execution_output.delta` event with the command Item ID and
stable output index. Draft Item output accumulates fragments; a supplied final
snapshot replaces it and is not emitted as another delta. Native output quotas and
text conversion apply, so the stream is not a byte-complete stdout/stderr capture.
Pinned native 0.153.4 can omit output emitted before its streaming subscription,
including from the eventual aggregate; this bridge cannot recover unobserved bytes.
That native gap remains open. Older peers may provide completion snapshots only.
Reasoning summaries and other
interim tool-output variants remain outside the supported surface.

Events publish only after their transaction commits. An idle Session keeps its
stream open for later Turns. Reconnection starts at the latest committed position,
including when Last-Event-ID is sent; it does not replay missed work. Connect,
buffer new events, then retrieve saved Session/Turn/Items state to recover. Deduplicate
by Item ID and retain finalized Items when applying buffered updates.

The internal buffer is limited to 256 events / 64 MiB per Session, with a single
oversized-event exception. A lagging reader receives a customer-safe `error` and
disconnects rather than silently skipping output. Slow socket writes time out
without blocking execution. Unsupported event variants are not implied by this endpoint.

Internal function execution uses the same native daemon harness, with resolved
non-deferred definitions and Store result admission. It verifies ordered text/image
results, error text, application receipts, matching action/Item call IDs, cancellation
and native Session continuity. Codex supplies the model transport's default image
detail. This proof uses a synthetic model responder and the real daemon/Codex;
the public workflow below exercises the same native bridge through HTTP. Deferred functions,
other tool kinds and the native 64-definition limit remain compatibility gaps.

Function-action read coverage uses persisted-call fixtures with the real service
handler, PostgreSQL and pinned official client. Call insertion and application
receipts update Turn/Session state atomically; duplicate notifications emit no new
state. Actions remain visible until the execution adapter acknowledges application,
or cancellation/terminal state removes them. This acknowledgement timing and the
exact sequence of repeated `requires_action` notifications are implementation
choices: the pinned source defines their shape but not that precise ordering.
Session state events contain `event_id`, `type` and `session`; Turn events retain
`session_id` and `turn_id`. There is no invented Turn `waiting` event.

Public function-result admission is verified with the pinned Python client and
raw HTTP against a dedicated PostgreSQL fixture: required fields, nullable output
and error, ordered text/image output, variant rejection, atomic batches, scoped
access and retries after terminal state. This admission verification complements the native public workflow below.
The generated Swagger 2.0 document leaves the output union unconstrained because
it cannot express string-or-content-array unions; the pinned upstream types and
server validation define the supported alternatives.

### Public function configuration

Inline `agent.tools` accepts non-deferred `function` definitions with the upstream
required name, description and JSON Schema parameter object. Missing
`defer_loading` resolves to `false`; null and other types are rejected. Omitted,
null and empty tool lists resolve to an empty list. The resolved tools are part of
the immutable Session configuration and creation retry identity. Saved-Agent
inheritance uses the same resolved tools. Deferred discovery, other tool kinds,
the native 64-definition cap and unique nonblank names of at most 512 bytes remain
compatibility gaps. Claude SDK additionally requires object-root schemas and
text-only results. Codex internal Goal/Skills/user-input/discovery semantics need
upstream evidence; their presence alone does not prove a tool-set mismatch.

The worker selects a same-tenant host advertising `function_tools` for configured
Sessions. Work remains queued when no compatible host is available, including
when a previously bound host no longer advertises that capability. It does not
silently discard the definitions or move an existing native Session.

`TestNativePublicFunctionExecution` runs `tests/official_functions.py` using the
pinned official SDK against the actual HTTP handler, worker, PostgreSQL, daemon
and Codex. A synthetic model requests a configured function; the client reads
`required_actions`, submits ordered text/image results through public events,
retries the same result, receives completion, and reuses the Session. A subsequent
Turn verifies error output, and a third verifies cancellation while waiting.
The test checks native result receipts, retained function Items and no duplicate
native continuation. This proves the implemented workflow, not compatibility
with every tool variant or the upstream service's exact event timing.

Accepted results currently enter public Items through native execution observations.
If cancellation prevents native application (for example, a result followed by
cancel in one admitted batch), the submission remains saved internally but has no
public result Item or `item.added`. Admission-time result indexing and unapplied
result recovery remain a separate compatibility gap; retries do not repair it.

`TestNativePublicFunctionStreamHelper` runs `tests/official_function_stream.py`
with the same native fixture and pinned SDK. `sessions.stream(tool_handlers=...)`
submits a mapping returned by a handler and a generic failure when the handler
raises. It verifies one invocation per call, retained output/error field presence,
native application, and termination after the matching Turn completes and Session
returns idle. These are controlled tests with synthetic model responses. Live
execution acceptance additionally requires a real model API; provider connectivity
alone does not prove the Agents API/daemon/harness workflow.

### Default verbosity on native models

The pinned `AgentTextParam` defines `medium` as the default text amount. Omitted,
null and explicit `medium` keep the same effective Session configuration and retry
identity. Supported native models receive the explicit requested level. For
unsupported or unknown models, the Codex adapter removes a `medium` override and
uses native defaults while preserving the requested model and catalog snapshot.
It still rejects unsupported `low`/`high` and unreadable catalogs.

This follows [Codex 0.153.4 request selection](https://github.com/openai/codex/blob/rust-v0.153.4/codex-rs/core/src/client.rs#L951)
and its [unknown-model fallback](https://github.com/openai/codex/blob/rust-v0.153.4/codex-rs/models-manager/src/model_info.rs#L134).
Controlled native verification checks explicit levels on supported models, absent
verbosity on an unknown model, initial/resumed Turns, and default retry equivalence.
This does not imply support for non-default verbosity on every model.

Live MiniMax-M3 verification used the pinned SDK, actual service/worker/PostgreSQL,
daemon and Codex with MiniMax's real Responses API. Two Turns verified a successful
function result, handler failure, retained result fields, stream termination and
native history continuity by recalling a random value returned only by the first
tool invocation. Omitted, null and explicit medium reused the same creation
identity. The tool data was synthetic; model responses were live. This does not
establish non-default verbosity, tool-set enforcement or full protocol conformance.

### Initial text at Session creation

Session creation accepts the pinned string and user-message-array input
forms. It shares text validation and admission with the events endpoint. The
Session and its initial work commit atomically; an identical
creation retry never re-admits the input, including after later or terminal Turns.
With `none`, this includes the first Turn and input Items. With `self_hosted`, it
includes the initial reservation and connection action; preparation and Turn
admission belong to the existing Worker. Creation returns while the executor is
offline, and an initial deadline failure leaves a failed Session without a Turn.
Omitted/null input creates an idle Session. Execution must be enabled and the
configured engine must support admission before any initial work is persisted.

Fixed SDK/raw HTTP and PostgreSQL tests cover the accepted forms, saved and inline
configuration, ordering, tenant isolation, retries, rollback and persistence.
Non-text input remains a gap. Empty arrays and blank text
currently fail the shared message validator; exact upstream handling of these
cases, local size limits and error details remains unverified. Swagger 2 cannot
express the string/array union, so input is unconstrained with a type description.

### Session creation streaming

`POST /v1/agents/sessions` also accepts `stream=true` for the supported creation
inputs. Fresh creation sends `agent.session.created` with the pre-input Session,
then its committed activity/Turn/Item/output events. Self-hosted initial creation
first requests the Environment connection, before native readiness and a Turn.
The cursor comes
from the atomic creation upsert, so rapid initial execution cannot move the start
past its own events. The ordinary bounded-buffer/gap policy still applies.

The local `Idempotency-Key` creation extension shares identity across response
modes. Retrying creation streams only future changes and never resubmits input or
replays old events. Recover a lost Session ID by repeating the same request/key
with `stream=false`, then use Session/Turn/Items reads. Disconnect only stops the
HTTP observer; committed reservations and admitted execution continue. Idle streams remain open for later
Turns. Pinned SDK3.13.0 proves the creation stream and created-event schema; exact
upstream initial snapshot/order, POST stream lifetime and retry behavior have not
been compared with the hosted service. These choices are not full conformance.

`official_session_creation_stream.py` covers the pinned client and raw HTTP on
real PostgreSQL: idle/initial text and saved Agents, first snapshots and ordered
Items, retries across response modes, later Turns, disconnect recovery, isolation
and errors before stream headers. Store tests cover concurrent upsert ownership,
pre-admission cursors and observers draining after execution has completed.

## Acceptance evidence and remaining scope

These accepted changes have distinct evidence levels. The associated PR records
include validation and limitations; later acceptance does not upgrade an earlier
controlled fixture into a real-provider test.

| Area | Evidence |
| --- | --- |
| Codex function stream/default text | [#544](https://github.com/MiniMax-AI-Dev/parsar/pull/544), [#545](https://github.com/MiniMax-AI-Dev/parsar/pull/545): fixed SDK/raw HTTP, actual PostgreSQL/daemon/native harness; #545 adds real MiniMax success/error and native history continuity |
| Independent build/container | [#552](https://github.com/MiniMax-AI-Dev/parsar/pull/552), [#563](https://github.com/MiniMax-AI-Dev/parsar/pull/563): isolated binaries/container, official Python/Go clients and real MiniMax execution across API restart |
| Session creation and source identity | [#564](https://github.com/MiniMax-AI-Dev/parsar/pull/564), [#567](https://github.com/MiniMax-AI-Dev/parsar/pull/567), [#572](https://github.com/MiniMax-AI-Dev/parsar/pull/572): atomic initial text, creation streaming and mutation-independent saved-reference retries |
| Saved resource lifecycle and Session filtering | [#573](https://github.com/MiniMax-AI-Dev/parsar/pull/573), [#574](https://github.com/MiniMax-AI-Dev/parsar/pull/574), [#581](https://github.com/MiniMax-AI-Dev/parsar/pull/581): official client/raw HTTP, PostgreSQL, tenant isolation and source mutation/deletion; #581 also filters completed real MiniMax Sessions |
| Claude SDK public execution | [#580](https://github.com/MiniMax-AI-Dev/parsar/pull/580): built API/registered daemon/packaged SDK with real MiniMax text, function success/error, active input, SSE/Items, pending-call cancellation and daemon cold continuation with retained native identity/history |

Principal workflows above are accepted within their profiles. Missing resources,
broader configuration/content, complete Usage provenance, unapplied result
visibility, exact hosted errors/event timing and crash-window reconciliation remain
open. Claude SDK raw usage is retained internally; public usage stays null without
a complete token breakdown. Neither successful cold continuation nor database
writer fencing proves recovery of interrupted native side effects. Full protocol
compatibility, other harnesses/platforms and Parsar cutover are not established.

### Caller principal foundation

Caller keys now resolve an explicitly configured organization/project and typed
user/service-account identity. An immutable project-to-tenant mapping is verified
against PostgreSQL before startup. Optional official organization/project headers
must match the key's authorized scope; ambiguous or conflicting headers use the
existing `401 invalid_api_key` response. This error policy is an implementation
choice, not verified hosted error parity. Project resource access remains shared
within the authorized project. New Sessions persist immutable creator kind/ID from
the authenticated principal; ordinary and streaming creation retries require the
same typed subject, including when recovering before saved-Agent lookup. Rotated
keys for that subject share retry identity. Unknown historical creators cannot be
claimed by retry. This local 409 policy is not verified hosted retry parity.
Creator fields remain internal and do not extend the public Session schema.
Executor keys now require the target Session's verified project and typed creator,
with optional exact-Environment restriction. Key issuance can precede Session
creation; rotation/revocation and current authorization reuse the durable ledger
and existing native registry. Historical keys remain revoked and unclaimed. This
executor-specific prerequisite does not open public Environment admission or
establish complete ownership, hosted key lifecycle or error compatibility. See the
[standalone configuration](../../services/agents-api/README.md#standalone-http-service).

Core documents its optional [harness selection extension](harness-selection.md) separately from the pinned upstream contract.

Model endpoints and credentials may be supplied at Session creation through the
[write-only execution extension](model-execution.md). Provider catalogs and their
business permissions remain client/product responsibilities.
