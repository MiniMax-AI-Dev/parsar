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
engine-specific shapes stay at the adapter boundary. Reuse the native Codex
app-server and exec-server protocols before adding another orchestration layer.
Verify configuration against actual execution: response defaults must not merely
describe values the adapter never applied.

The next slices are contract conformance checks, effective configuration and the
adapter boundary, remaining function/tool variants, then environment/file resources.
Reusable Agents, vaults and protocol subagents remain in the coverage backlog.
Parsar cutover follows an independent client workflow; its business Team loop is
separate. Each slice can use multiple small PRs tracked in the Feishu board.

## Upstream resource inventory

This inventory is based on the pinned Python source, not our generated OpenAPI.
It contains 42 distinct HTTP operations in 15 resource classes, excluding async
duplicates, overloads and client-side helpers. Nine operations currently have
handlers; that count is not a compatibility score. Even those operations implement
only part of the upstream input, configuration and event variants.

Paths below are SDK resource paths beneath `client.beta.agents`. Method names use
the Python SDK. Vault HTTP paths start at `/vaults`, not `/agents/vaults`.

| Resource | Upstream operations | Current coverage |
| --- | --- | --- |
| Root reusable Agents | create, retrieve, update, list, delete | Missing |
| sessions | create, retrieve, update, list, delete | Partial create/retrieve/list; metadata update implemented; delete missing |
| sessions.events | create, stream | Text/cancel/function-result admission and live events; function-action state snapshots supported |
| sessions.turns | retrieve, list | Implemented reads; lifecycle conformance still partial |
| sessions.items | list | Partial Item variants |
| sessions.artifacts | retrieve, list, delete, content | Missing |
| sessions.subagents | retrieve, list | Missing |
| sessions.subagents.items | list | Missing |
| sessions.subagents.turns | retrieve, list | Missing |
| sessions.subagents.turns.items | list | Missing |
| environments | retrieve | Missing |
| environments.files | create, list | Missing |
| environments.templates | create, retrieve, update, list, delete | Missing |
| vaults | create, retrieve, list, delete | Missing |
| vaults.credentials | create, retrieve, update, list, delete | Missing |

For each resource, verify the referenced request/response unions and observable
behavior, not just the route. Creation streaming/initial input, configuration
options, text/image content, function results, environment variants, full Item/SSE
variants, defaults, field omission/nullability and errors need their own cases.
Use strict official-client tests plus raw HTTP assertions; SDKs can accept extra
fields and cannot prove that reported configuration matches the running engine.
Where SDK types or public documentation do not establish behavior, record the
uncertainty and obtain upstream evidence before marking it conformant. Temporary
unsupported errors are implementation gaps, never evidence of full compatibility.

## Public semantics

- Use `/agents/sessions` beneath the configured API base URL, bearer authentication
  and `OpenAI-Beta: agents=v1`. Do not introduce a competing `/sessions` surface.
- Session creation takes an environment and inline agent configuration or a saved
  agent reference. A model name is not a daemon engine name.
  In the pinned `session_create_params.py`, `stream` defaults to false and neither
  `stream` nor `agent_id` permits null. Metadata omission/null defaults to an empty
  map; individual values must be strings, including valid empty strings. Validate
  these distinctions before persistence rather than coercing null to Go zero values.
- `POST /agents/sessions/{id}` updates metadata only: omission preserves it,
  null or `{}` clears it, and an object replaces all pairs. Apply the same string
  and character limits as creation. Preserve execution state, effective configuration
  and the original creation retry identity. Fixed SDK/raw HTTP checks cover these
  distinctions, tenant isolation, active Session reads and restart persistence.
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

## Delivery and verification

| Capability | Current state |
| --- | --- |
| Shared daemon connection layer | Implemented; existing product protocol retained |
| Tenant-scoped Session persistence | Implemented; internal Store, not a public API |
| Effective Session configuration persistence | Implemented; immutable JSON snapshot and retry identity |
| Authenticated Session HTTP API | Create/retrieve/list and metadata-only update; inline model/instructions, ordinary text with low/medium/high verbosity (Unix daemon; non-default levels require native model support), non-deferred function tools, environment `none`, metadata and creation retry keys |
| Internal Turn/input persistence | Tenant-scoped atomic message/cancel/function-result batches, steering, request-level retry identity, cancellation targets and terminal outcomes; public function-result decoding and mixed-batch admission supported |
| Internal Turn execution | Bound daemon dispatch, strict native resume, receipt-based steering/cancellation; explicit Codex no-environment execution and disabled native subagent tools for the resolved false default; public text/cancel submission with a bounded standalone worker |
| Neutral tool observation transport | Codex opt-in capability normalizes current tool snapshots in the daemon; Agents API admission/projection has not switched to this mode |
| Internal execution observations | Ordered durable text/tool/usage journal and terminal outcome; partial cancellation output retained; optional native message IDs/phase/completion via `message_items` and tool snapshots via `tool_items`; tenant-scoped paginated Store reads |
| Public Turn recovery | Retrieve/list persisted states with scoped pagination; see limitations below |
| Public Items recovery | Indexed message/command/MCP/function/web-search reads, scoped pagination and restart recovery; limitations below |
| Public SSE | Live Session/Turn lifecycle, supported Item and text events; bounded commit-before-publish buffering and recovery through saved reads |
| Internal function bridge | Native Codex definitions and ordered text/image/error results, persisted callbacks and application receipts, cancellation and native resume; public non-deferred function configuration supported |
| Function-call persistence and reads | Immutable scoped calls/results/receipts; Session `required_actions`, `requires_action`, Turn `waiting` and live state snapshots; internal native dispatch integrated; public non-deferred function configuration supported |
| Pending actions and environment lifecycle | Pending |
| Official-client compatibility | Strict SDK checks for Session/Turn/Items reads and native text execution/cancellation/verbosity; pagination, retries, errors, tenant isolation and recovery |
| Go product client | Official `openai-go` v3.61.0 with a thin service configuration; real HTTP integration tests |
| Product cutover | Pending |
| Team orchestration | Deferred; Parsar-owned |

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
specification. The shared Go wire types are in `v1`. Session update/delete, saved
Agent references/filtering, structured output, other agent options, vaults, initial input, creation streaming
and execution/provider resources are not supported by this slice. Reject them
explicitly. `AGENTS_API_ENGINE` selects the service's engine independently of the
requested model; it does not add a competing field to the upstream request.

### Native subagent control

The pinned `types/beta/multi_agent_config.py` defines `enabled=false` as disabling
subagent tools. The dispatcher enforces that effective value with a typed daemon
policy and capability admission; the Codex adapter applies native feature controls
on fresh and resumed Turns. Operator feature preferences cannot re-enable them.
Controlled model-boundary tests check absence of direct/deferred subagent tools
while the official function workflow continues to run.

Explicit inline `multi_agent` input, enabled multi-agent execution and public
Subagent resources are still unsupported. Do not infer that `Agent.tools` is the
complete native tool registry: environment and subagent tools have separate
configuration. The upstream behavior of internal Goal, Skills and user-input
utilities needs further evidence; their presence alone is not proof of a mismatch.

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
that the Turn failed. Native start/completion snapshots are available, but interim
tool-output deltas are not yet captured. Tool output is visible to the Session's
authenticated tenant and may include the command's or tool's own diagnostic text.

The index rebuilds pre-migration history from saved observations on first access,
in pages under the Session lock. Large historical Sessions can make that first
access slower. Subsequent reads use the durable index. Legacy unkeyed text is a
single aggregate: original native message boundaries cannot be reconstructed.
Legacy tool results retain their content but have `incomplete` status when the
source did not record a native outcome. Only recognized historical user text/image
shapes become messages; arbitrary internal input objects remain in source storage.
Unsupported native variants, reasoning, subagent Items and Items mutation
are not covered. Public submission supports text messages, cancellation and function results.

Legacy Done frames alone do not complete assistant Items. Aggregate answer text
is confirmed by successful Turn termination; failed Turns retain observed deltas
instead of treating adapter diagnostics as assistant output.

Pagination orders by first-observation timestamp, then the Item's immutable
Session position and public ID. New Items retain observation order even when
timestamps match. The index also stores a zero-based output index per Turn for
future streaming; inputs do not consume it. Updates and retries do not move Items
or change output indexes. Existing indexed history retains its pre-upgrade
deterministic order rather than guessing an unavailable original source order.

### No-environment execution

The internal dispatcher can execute a public `environment.type=none` snapshot
on an authenticated, bound engine host advertising `environment_none`. It does
not allocate a local execution environment: the daemon uses upstream Codex's
`CODEX_EXEC_SERVER_URL=none` and verifies the native environment state before
starting/resuming. A missing capability or unsupported native method fails rather
than falling back to local execution. Private `daemon` snapshots remain distinct.
This is an engine tool/environment boundary, not operating-system isolation.
The standalone worker uses this mode for public text and function execution. The self-hosted
registry/Noise transport remains pending.

The native reference is Codex `rust-v0.153.4`, commit
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`, especially
`codex-rs/exec-server/src/environment_provider.rs`. The self-hosted registry
requires executor registration, harness authorization and encrypted relay;
a daemon WebSocket URL is not that protocol.

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

The service takes a database advisory lease, so a second execution service cannot
start on the same database. Startup marks previously claimed Turns failed without
replaying them and retains queued work. This does not recover missing daemon frames
or guarantee exactly-once external side effects. Session status reflects the latest
persisted Turn; usage reports recorded measurements.

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
`incomplete` status. Tool snapshots are supported; native interim command-output
and reasoning deltas remain outside the supported surface.

Events publish only after their transaction commits. An idle Session keeps its
stream open for later Turns. Reconnection starts at the latest committed position,
including when Last-Event-ID is sent; it does not replay missed work. Connect,
buffer new events, then retrieve saved Session/Turn/Items state to recover. Deduplicate
by Item ID and retain finalized Items when applying buffered updates.

The internal buffer is limited to 256 events / 64 MiB per Session, with a single
oversized-event exception. A lagging reader receives a customer-safe `error` and
disconnects rather than silently skipping output. Slow socket writes time out
without blocking execution. Creation streaming and unsupported event variants
are not implied by this endpoint.

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
the immutable Session configuration and creation retry identity. This slice does
not implement saved-Agent inheritance or deferred tool discovery. The native
64-definition cap, unique nonblank names of at most 512 bytes, other tool kinds
and unrestricted JSON Schema execution remain compatibility gaps. Codex also
exposes native planning/goal/skill/discovery tools; restricting those to the
effective public tool set is an outstanding adapter gap, not implied here.

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
