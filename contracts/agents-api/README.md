# Agents API contract

The external reference is [openai-python beta/agents](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents),
pinned in `upstream.json`. Its resource methods, corresponding types, pagination
and streaming helpers define the compatibility target. This directory records
the boundary; it does not imply that every upstream feature is implemented.

Parsar owns product Agents and Teams. This service owns execution configuration
snapshots and single-Agent sessions/turns. The OpenAI Agents Python **SDK** is a
separate future dependency for Team orchestration in Parsar, not the HTTP contract.

## Public semantics

- Use `/agents/sessions` beneath the configured API base URL, bearer authentication
  and `OpenAI-Beta: agents=v1`. Do not introduce a competing `/sessions` surface.
- Session creation takes an environment and inline agent configuration or a saved
  agent reference. A model name is not a daemon engine name.
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
| Authenticated Session HTTP API | Create/retrieve/list; inline model/instructions, environment `none`, metadata and creation retry keys |
| Internal Turn/input persistence | Tenant-scoped atomic input batches, steering, request-level retry identity, cancellation targets and terminal outcomes |
| Internal Turn execution | Bound daemon dispatch, strict native resume, receipt-based steering/cancellation; explicit Codex no-environment execution; public text/cancel submission with a bounded standalone worker |
| Internal execution observations | Ordered durable text/tool/usage journal and terminal outcome; partial cancellation output retained; optional native message IDs/phase/completion via `message_items` and tool snapshots via `tool_items`; tenant-scoped paginated Store reads |
| Public Turn recovery | Retrieve/list persisted states with scoped pagination; see limitations below |
| Public Items recovery | Indexed message/command/MCP/function/web-search reads, scoped pagination and restart recovery; limitations below |
| Public SSE | Pending; internal daemon payloads are not upstream wire objects |
| Pending actions and environment lifecycle | Pending |
| Official-client compatibility | Strict SDK checks for Session/Turn/Items reads and native text execution/cancellation; pagination, retries, errors, tenant isolation and recovery |
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
Agent references/filtering, other agent options, vaults, initial input, streaming
and execution/provider resources are not supported by this slice. Reject them
explicitly. `AGENTS_API_ENGINE` selects the service's engine independently of the
requested model; it does not add a competing field to the upstream request.

### Turn recovery reads

`GET /v1/agents/sessions/{session_id}/turns` and retrieval by `turn_id`
return persisted Turn states using the pinned official client contract. Lists
support `after`, `limit` (1..100, default 20), and `order` (default `desc`).
The cursor is a Turn ID in the same tenant and Session. Failed turns expose a
generic `internal_error`, never raw engine diagnostics. `usage` is currently
null because the native record does not guarantee the required cache/reasoning
breakdown; raw measurements remain in execution storage. Session runtime state derives from the latest Turn. Public SSE remains pending.

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
Unsupported native variants, reasoning, subagent Items, Items mutation and live SSE
are not covered. Public submission currently supports text messages and cancellation.

Legacy Done frames alone do not complete assistant Items. Aggregate answer text
is confirmed by successful Turn termination; failed Turns retain observed deltas
instead of treating adapter diagnostics as assistant output.

Pagination orders by first-observation timestamp, then position within that source
and stable public ID. Distinct observations sharing exactly the same timestamp
may therefore differ from journal order; preserving source order for that tie is
a tracked follow-up. Content updates do not move existing Items.

### No-environment execution

The internal dispatcher can execute a public `environment.type=none` snapshot
on an authenticated, bound engine host advertising `environment_none`. It does
not allocate a local execution environment: the daemon uses upstream Codex's
`CODEX_EXEC_SERVER_URL=none` and verifies the native environment state before
starting/resuming. A missing capability or unsupported native method fails rather
than falling back to local execution. Private `daemon` snapshots remain distinct.
This is an engine tool/environment boundary, not operating-system isolation.
The standalone worker uses this mode for public text execution. The self-hosted
registry/Noise transport remains pending.

The native reference is Codex `rust-v0.153.4`, commit
`3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`, especially
`codex-rs/exec-server/src/environment_provider.rs`. The self-hosted registry
requires executor registration, harness authorization and encrypted relay;
a daemon WebSocket URL is not that protocol.

### Public execution admission

`POST /v1/agents/sessions/{session_id}/events` accepts `agent.session.input.message`
with user `input_text` content and `agent.session.input.cancel`. Successful atomic
admission returns 204, as consumed by the official `events.create` method. A retry
key identifies the entire ordered request; conflict does not partially admit it.
Messages start queued work or steer the active Turn. Individual input messages
remain distinct Items even when their text shares one native prompt.

Enable the standalone daemon gateway to run the worker; without it, admission
returns 503. The worker selects capable same-tenant engine hosts, binds each Session
once, and runs at most four Turns concurrently. Queued cancellation needs no live
engine. Active cancellation waits for a native receipt; terminal completion can
win that race. Query Turn/Items to recover results; live SSE is not implemented.

The service takes a database advisory lease, so a second execution service cannot
start on the same database. Startup marks previously claimed Turns failed without
replaying them and retains queued work. This does not recover missing daemon frames
or guarantee exactly-once external side effects. Session status reflects the latest
persisted Turn; full usage breakdown and pending actions remain unimplemented.

Native verification uses `PARSAR_NATIVE_DAEMON_BIN`, `PARSAR_NATIVE_PROOF_DIR` under
`~/.parsar/`, and `PARSAR_OFFICIAL_SDK_PYTHON` pointing to the pinned SDK environment.
The Store native integration test runs `tests/official_execution.py` against a real
HTTP handler, PostgreSQL, daemon and Codex with a synthetic model provider.
