# Contributing to Parsar

Parsar is an open-source agent collaboration control plane for engineering
teams. This guide covers the rules that apply at every stage of development —
read it before opening a PR.

## Hard rules

- All runtime config, logs, state, and cache must be written under
  `~/.parsar/`.
- Never write runtime state to the repo root or the current working directory.
- A user-supplied working directory must be an absolute path or start with
  `~/`. Reject relative paths outright — do not resolve them against the CWD.
- Before any install / setup step, ask yourself: does this write to the
  user's current directory? If yes, fix it before shipping.
- Before any substantial implementation, refactor, runtime/process change,
  schema/API change, or cross-package behavior change, read this guide. If
  the change creates, removes, or clarifies an architecture rule, ownership
  boundary, workflow, required check, or generated artifact contract, update
  this guide in the same branch.
- Keep contributor docs concise and single-sourced. `AGENTS.md` is only the
  agent-facing shortcut; canonical rules live here. When two documents repeat
  the same long-form rule, delete the duplicate and link to the canonical
  section.

## Worktree workflow

All code changes — features, fixes, refactors, documentation that references
code paths — must happen in a git worktree branched from `main`. **Direct
commits to `main` are forbidden.**

`main` is the single integration baseline:

- A new worktree must branch from the latest `origin/main`. Run
  `git fetch origin main` first.
- `main` is the source of truth. Every worktree starts from it and lands
  back into it.
- After implementing and verifying, push the feature branch and open a PR
  against `main`. **Merging into `main` requires PR review** — local
  fast-forward or local merge to bypass review is not allowed. Self-review
  qualifies when the developer judges it sufficient under the review policy below.
- Run `make check` (and any relevant E2E target) before requesting review.
- Place worktrees under `.worktrees/<feature-name>/` so they don't litter
  the repo root.

```bash
git fetch origin main
git worktree add .worktrees/feature-name -b feature/name origin/main
```

Direct development on `main` is not allowed. Every session honours this rule.

## Independent blind review

Each PR should deliver a bounded, independently usable and verifiable capability.
Combine closely related changes that share initialization and security boundaries;
Environment Template follow-ups should not split by field or internal wiring step.
Separate work with independent risk or an unresolved design. Run focused tests
during development, then complete full checks, real regression and the required
review once the scope stabilizes, without repeating that gate for every substep.
State acceptance results, the scope ceiling, exclusions and stopping conditions
before implementation. Batch size must not weaken security or data consistency.
Check uncertain design choices early. Reassess any new prerequisite against those
results before adding it; do not let a functional batch grow without a stopping
point. Keep unrelated refactors, features,
formatting, and dependency updates in separate PRs.

The developer chooses the review method based on scope, risk, uncertainty and
expected value. Self-review with suitable verification is acceptable when it is
sufficient. Use an independent assessment when it would materially help, especially
for security-sensitive, shared or uncertain behavior; a subagent is not mandatory
for every change. For substantial or uncertain designs, independent subagents may
propose and challenge alternatives before implementation; the main thread chooses
by evidence, scope and cost. All built-in subagents, including design, development,
validation and blind review, must explicitly use GPT-6 Astra (`gpt-6-astra`) with
`high` reasoning effort.

When using blind review, finish implementation and required checks first, then ask
one fresh subagent in an independent context to review the entire diff. Give it
only requirements, acceptance criteria, project rules, scope boundaries, repository
location and comparison baseline. Do not provide the developer's conversation,
implementation summary, self-assessment, suspected defects or previous findings.

Evaluate suggestions by evidence, actual risk, task scope and ROI. Low-value or
out-of-scope suggestions may be declined; record real deferred issues without
expanding the PR to address every comment. Address in-scope blocking findings and
rerun relevant checks, then decide whether a fresh independent review adds value.
Required checks still apply to every PR. Merge through the PR once checks pass and
blocking findings are resolved. Record the review method, verification, material
deferrals and PR outcome in the linked issue.

If review and fixes keep cycling, stop patching and reassess the design. If
the design still does not converge, document the unresolved problem and defer
that issue instead of expanding the PR. Continue with independent issues.

## Architecture baseline

- **Server**: Go + Chi.
- **Database**: PostgreSQL only.
- **DB toolchain**: goose migrations + sqlc-generated queries + pgx/pgxpool
  at runtime.
- **Web**: Vite + React SPA, eventually served directly by the Go server.
- **API**: OpenAPI-first.
- **Execution client**: the product registers only `connector_type=agents_api`
  and uses `packages/agents-client/v1` with the official OpenAI Go SDK.
  Core owns daemon connections, engine selection and environments.
  Product image builds include `contracts/agents-api/v1` for shared public
  configuration validation, alongside the client package.

## Architecture boundaries

The repo has several concepts that sound similar but must stay separate.
When adding or changing code, name the boundary explicitly in the PR
description and keep ownership on the side listed here.

### Product and execution service separation

Agents API is the primary infrastructure deliverable. Parsar is an ordinary client
and example application; its feature backlog must not dictate the execution
service's public protocol or internal model. Agents API must build, deploy and run
without the Parsar product service, frontend or database. An optional Compose
deployment may install both services with one PostgreSQL instance, but separate
databases, credentials and migrations. The product uses Core exclusively; it has no native daemon or HTTP Agent fallback.

#### Design and compatibility requirements

- The complete pinned `openai/openai-python` `beta/agents` protocol is the target,
  including its referenced resources and types. Match paths, methods, headers,
  field presence, nullability, discriminators, defaults, status transitions,
  pagination, errors and streaming behavior. Engine limitations are implementation
  gaps to solve, not grounds for narrowing or redefining the upstream contract.
- Pin upstream source and SDK versions in `contracts/agents-api/upstream.json`.
  Use official SDKs for clients and reuse upstream types or schemas where suitable.
  SDK deserialization alone is not server validation or proof of compatibility:
  test raw HTTP payloads and observable workflows as well. Synthetic data and mock
  model responses may support controlled tests; live execution acceptance must
  call a real model API through the service, daemon and harness. A real daemon
  with a synthetic model does not constitute live model validation. Keep provider
  credentials in private test configuration, outside source, logs and task records.
  Record unspecified or unverified behavior explicitly; never invent official
  semantics. Track partial
  coverage in `contracts/agents-api/README.md` until the complete target is verified.
  Reconcile current coverage summaries with merged routes and recorded acceptance;
  distinguish accepted profiles, partial implementation, missing operations and
  unverified semantics. Retain historical evidence with its original scope. Handler
  counts are not compatibility percentages, and an active provider probe is not
  deployment qualification.
- No legacy Agents API compatibility requirement takes precedence over this
  design. Replace an unsuitable implementation instead of growing compatibility
  branches. Preserve reusable, verified infrastructure rather than rewriting it
  merely for new names or directories. Replacements may retire obsolete private
  interfaces and history backfills in bounded PRs; this does not authorize deleting
  product data or changing unrelated product behavior.
- Keep engine-specific types, process management and protocol translation inside
  execution adapters. The public API and persistence/application core must not
  interpret Parsar product payloads or depend on one engine's native item types.
  Prefer maintained upstream SDKs and native execution protocols over a second
  hand-written model/tool loop or a general-purpose compatibility framework.
- Verify an independent official-client workflow before a Parsar integration.
  Parsar uses the same public contract as any other client, with no privileged
  endpoint or direct execution-table access. An OpenAI endpoint is a possible
  client target only where the requested capabilities and credentials support it.
- Maintain tasks, priorities and evidence in the Feishu board. Register issues
  discovered during a task without switching work or automatically selecting them
  next. Only a direct acceptance blocker justifies a minimal in-scope fix. After
  each bounded task passes checks/review and merges, mark it done, reread the full
  board and choose the next task by value, dependencies, risk and effort. Agent API
  protocol and atomic execution work takes priority over product integration, UI
  work and business Team orchestration. Prioritize a sound architecture skeleton
  and correct principal workflows with real API validation. Record and defer
  low-frequency corner cases when risk and ROI permit; do not let minor details
  delay the main work. Required checks and material correctness guarantees apply.

- Parsar owns users, workspaces, business authorization, Agent/Team definitions,
  capabilities, product conversations, IM/sharing, approval decisions and billing.
- Agents API owns protocol saved Agents, execution sessions/turns, effective
  configuration snapshots, dispatch/cancel, environments, vaults, raw usage,
  pending interactions, protocol subagents and durable events. Protocol saved
  Agents/vaults are execution resources, not Parsar marketplace or business roles.
  Neither service reads the other's tables. Parsar uses a versioned client contract.
- A product conversation may map to several execution sessions. An execution
  session is distinct from a live daemon socket, process or sandbox. Native engine
  session identifiers belong to the execution service.
- Establish single-Agent execution, approval, cancellation, idempotent submission,
  persisted recovery queries before Team orchestration. The upstream SSE stream
  is live-only; recover through Session/Turn/Items reads. Any additional product
  cursor replay must be documented as an extension, not upstream semantics.
  Team definitions, management and orchestration belong to Parsar. Agents API
  establishes single-Agent execution first; business Team loops are deferred.
  This does not exclude upstream `multi_agent` configuration or subagent resources
  from protocol coverage. Future business Team orchestration directly depends on
  `openai/openai-agents-python` in Parsar.
- Daemon Skill/SP authoring remains a product operation: forward through a scoped
  product callback with the original requester and workspace checks. A runtime
  credential alone must not grant business write permissions.

#### Environment ownership and placement

Environment identity, tenant/Session association, configuration and lifecycle belong
in Agents API, independently of provider compute, authenticated device identity,
daemon sockets and native harness Sessions. Create an Environment association in
the same transaction as its Session and creation identity when this resource is
implemented. Keep mutable connection/registration state out of immutable
configuration; replacement ownership must fence stale observations.

The current MVP covers Codex, Claude Code and MiniMax Code through the shared
single-Agent path: Session
creation, environment preparation, native execution, files/artifacts, cancellation,
reconnection/recovery queries, and standalone deployment acceptance. Select each
bounded task from the complete board; nonblocking local improvements stay queued.
Authentication, tenant/credential isolation, state consistency and data loss remain
material acceptance requirements. Optional feature equality is not required. After
the three profiles pass merged-main validation, publish the results, limitations
and backlog, then stop development until new user direction. Additional harness
implementations and protocol Subagent execution remain queued without changing the
complete pinned protocol target.

The current hosted architecture is V1: Core runs independently; each Environment
sandbox contains its daemon, selected native harness, local tools and workspace.
Execution and Files use the same authorized workspace through the existing
Core/Runtime contract. Native tool calls stay local.
CLI discovery uses a bounded 15-second version probe per installed harness;
missing binaries fail immediately. A version result is availability, not Environment
readiness, and does not change initialization or connection ownership. Process placement and native
transport remain adapter responsibilities, without a second model/tool loop.
The former separated Runtime/harness and workspace executor topology is a distant
future V2 option, to revisit only after V1 is stable and concrete needs justify it.
Do not extend that topology for hosted delivery, maintain two current hosted routes,
or introduce dormant V2 compatibility scaffolding.

When an execution Session has a previously started Turn but no recorded native
Session ID, Core requires existing-history recovery through a verified Runtime
capability. Read that condition before claiming the next Turn. A supplied native ID
remains authoritative. The Codex adapter may recover only a unique, nonarchived
root in the exact Session-private native home and expected working directory, using
native listing and exact-ID resume. Missing, incomplete or ambiguous history must
fail without starting a fresh root. A recorded start can precede native work; that
uncertain case also fails conservatively. Recovery does not replay interrupted
inputs, erase prior outcomes or promise transparent continuation of running tools.
Keep Device identity and Environment scope in `ExecutionDevice`; native Session
identity and prior API Turn state belong to `SessionExecutionBinding`.

Platform-managed and user-managed deployment reuse this same Runtime. For platform
management, SandboxProvider creates and reclaims it. For user management, the user
starts the Runtime and its daemon authenticates and initiates the Core connection;
Core must verify tenant ownership and the exact Environment binding. These are
management responsibilities, not separate execution architectures. User-managed
Runtime does not automatically mean the official `self_hosted` discriminator;
that mapping needs separate protocol definition and acceptance. User-managed
installation and enrollment remain later board work, outside the current Docker
co-location security qualification.

Preserve the accepted official `self_hosted` interoperability path and its native
executor connection flow. Codex registry/Noise is specific to that path, not the
V1 hosted backbone or a universal protocol for all engines. A private daemon URL
or an undocumented daemon installation requirement cannot replace `remote_url`.
Keep harness cwd separate from the executor workspace where that accepted remote
path still requires it.

Public Environment Templates belong to Core and its execution database, independently
of provider image/build templates. Resolve a tenant-owned reference once at Session
creation, freeze the effective ordinary hosted configuration and reuse inline
initialization. Do not pass template IDs into Provider or Runtime. Omitted network
inherits; overrides may only narrow policy. Preserve unresolved caller intent for
creation retries and recover committed results before reading mutable templates.
Updates and deletion cannot rewrite existing Session snapshots. Initial files use
one Core-owned installer for template and inline configurations. Keep confidential
bytes encrypted under the execution-service key and resource-bound AEAD, separately
from ordinary configuration and public metadata. Templates retain source references;
Session creation freezes tenant-authorized source bytes in the same commit, independent
of later source/template deletion. Public resource reads must not require decryption
or load encrypted file bodies. Record original creation intent before resolution.

Template parsing, persistence and resolution must not select a harness or Provider,
or depend on native tool names and private harness paths. The shared initializer
uses the packaged Runtime contract for trusted commands, workspace/staging paths,
confidential input and completion receipts. Each Provider supplies that same
Runtime and carries initialization commands through RunCommand; each adapter owns
native tool configuration. A new harness or Provider must not require template
business-logic changes. Reuse qualified shared helpers even when their executable
names have historical engine prefixes; renaming is not a boundary fix. Select real
regressions by the changed shared, Provider and adapter boundaries, rather than
repeating every deployment combination for each configuration field.

The allocation lifecycle owns pending/running/complete initialization. Authentication
may connect the daemon during initialization; execution bindings, native preparation,
live Files and connected publication wait for completion. Keep Provider bootstrap
settlement distinct. Advance at most one bounded initialization operation per full
maintenance scan,
using process-local progress and the existing lifecycle gate. A recovered or uncertain
running installation fails and uses existing cleanup, without replaying writes.
Completed environments never reinstall initial files on reconnect or native recovery.
Provider RunCommand carries bounded stdin, not confidential argv. Only fixed trusted
initializers may run with Runtime authority. User setup and package install hooks
run in the common packaged sandbox, without daemon credentials or native history.
Files and inline Skills precede system, npm/Python packages and ordered setup commands. Initialization has
provisioning network access; requested network restrictions apply to native tools
after setup. Confidential env and setup snapshots are encrypted independently of
ordinary metadata. Adapters apply tool env only after isolation, never to the
credential-bearing daemon/native harness launcher.
Reuse the packaged atomic file writer and anchored parent creation across all profiles.

System packages use one Runtime-owned tool root, separate from trusted daemon and
harness executables. Build its immutable seed from the base image before adding
Runtime/harness code or secrets; include the matching package database and base
tool symlink targets. The shared installer extracts independent inodes and runs
apt/dpkg inside an unprivileged namespace. Package scripts cannot access Runtime
credentials, native history or outer processes. Later setup and native tools enter
the installed root read-only, retaining the authorized workspace and adapter-owned
scratch. `/workspace` and `/environment/workspace` refer to the same authorized
workspace inside that root, preserving native working directories. Native adapters
own entry and existing process cancellation; Core never
selects an engine or Provider for package initialization. No live filesystem
snapshot, second lifecycle owner or package-manager framework is introduced.
Core preserves the system-package requirement in the common execution binding;
a missing installation receipt fails preparation instead of falling back to base
tools. This requirement does not add execution prerequisites to Files reads.

Inline Skill ZIPs use the same confidential initialization snapshot and installer.
Core validates portable manifests and bounded regular-file archives, returns only
safe Skill metadata, and freezes content before native preparation. The Runtime
owns `/environment/initialization/capabilities/skills/<name>`; setup and native tools may read but
not modify this tree. The common execution descriptor carries Skill metadata,
never native plugin configuration or template identities. Adapters register native
Skill roots without changing the execution loop or enabling unrestricted tools.
Native activation extensions remain adapter-owned and must fail explicitly when
unqualified. Skills API references, generic Plugins and capability-directory
imports remain separate work; an adapter-owned Claude plugin envelope does not
implement public Plugins.

Name, enabled/disabled network, initial files, inline Skills and env/setup/system/npm/Python are
implemented independently of remaining installation fields. Reject unsupported
inputs rather than persisting them for silent
omission; expand inline and template initialization together in separately qualified
batches. Resource reads need only tenant authorization, not a live Runtime.
See the [Template coverage and unresolved semantics](contracts/agents-api/environment-templates.md).

SandboxProvider has five operations: Create, GetInfo, Renew, Kill and RunCommand.
Use maintained provider SDKs and thin adapters, Docker first and E2B after the MVP.
Provider initialization creates the sandbox and starts its daemon/harness;
RunCommand is for initialization only. Daily execution and Files use Runtime and
native or bounded local capabilities. Docker's lack of a native renewable lease
does not remove service-owned hosted expiry and cleanup requirements.

The E2B Provider uses an explicit `templateID:build_UUID` and the same qualified
colocated Runtime. Its root-private bootstrap input and final atomic receipt live
on persistent disk, never template `/run`. Running compute alone does not establish
completed initialization. Inspect exact installation/tenant/Environment/allocation
metadata and the matching Session/device receipt; never replay uncertain Create or
bootstrap. Credentials stay out of provider metadata, template environment and
command arguments. Use the existing one-hour disconnect grace with an E2B lease of
at least two hours. Expiry, pause or lost state cannot silently recreate/resume a
VM. Before launching daemon, trusted root bootstrap must correct E2B's writable
program/boot paths and disable its unused passwordless privileged account; qualify
these protections after provider finalization, not just in the source image.
The [E2B operator guide](services/agents-api/deploy/e2b/README.md) owns packaging,
configuration and real-cloud acceptance. The pinned official envd process schema
and generated Go messages live together under `internal/sandbox/e2b/envdprocess`;
regenerate with the documented tools when that source changes. Do not hand-write
Connect framing or add SDK subprocesses to the static Core. Provider envd file and
command access is initialization-only; public Files and execution remain on Runtime.

The independent Docker Provider consumes an immutable Runtime image and retains
one caller-owned allocation reference through partial creation and cleanup. Persist
that reference before Create and serialize its lifecycle; resolve a lost response
with observed state, without rewriting bootstrap credentials or replaying startup.
Provider state is compute state, not public Environment readiness. Named Runtime
volumes need explicit owned cleanup after container removal. A second mount of the
same workspace volume subdirectory provides the public `/workspace` path to native
tools; trusted staging and atomic rename retain the original parent mount. Do not
copy files or widen private-path reads to preserve an alias. Initialization command
timeouts can leave processes alive and require allocation cleanup before reuse.
The [managed Runtime build and operator configuration](services/agents-api/deploy/codex/README.md#managed-runtime-image-and-docker-adapter)
defines the explicit opt-in for basic hosted admission. Building an image alone
does not qualify its isolation or enable public creation.

Managed Runtime allocation, dedicated daemon credential hash and exact Session
binding commit atomically before Provider.Create, using the existing execution
lease and Session lock. Only the fresh allocation receipt permits Create; retries
and Core restart observe that same reference without replay or credential rotation.
The operator's stable provider key identifies one backend/installation; retain its
adapter for cleanup, and use a different key when changing the target. Never treat
absence on another backend as successful reclamation.
Disabling the default provider stops new hosted admission/bootstrap; it must not
block existing Session cancellation, tool results or input retry outcomes.
Input HTTP response budgets follow the persisted Environment type, covering the
admission wait for both hosted and self-hosted Sessions independently of operator
creation switches or remote executor configuration.

With an explicitly configured default managed provider, the same Worker scans
committed pending hosted Environments that have no allocation. This includes idle
Session creation and recovery after commit-before-bootstrap interruption; an
existing allocation never enters that startup path. Keep the scan bounded and
serialized by the existing lifecycle owner. Hosted provisioning requires no caller
connection action. An initial reservation without a Turn leaves its Session idle,
as allowed by the pinned contract; do not emit an in-progress event before a Turn
starts or treat a daemon connection as native readiness.

The same serialized scan publishes authenticated connection observations using
the existing durable generations after verifying the exact Session/device binding
and settled bootstrap. Socket loss remains observable during a provider outage;
Core restart fences old observations. Do not create a separate connection owner.

Terminal managed cleanup atomically revokes authority, persists Environment failure
or expiry, settles pending input and requests cancellation before external cleanup.
Preserve original input deadlines and retry outcomes. Temporary provider outages,
unknown Create results and stopped compute do not prove permanent failure. The
pinned stream has no Environment expired event; do not invent one. Exact hosted
failure codes and ordering remain explicitly unverified.

Allocation state is private compute ownership, separate from public Environment
connection/native readiness. Adapters qualify bootstrap completion; Core does not
infer it from an engine or provider name. Connected, observed compute receives
service keepalives between Turns. Keepalives cannot revive a one-hour lapse or a
cleanup request. Idle alone never requests shutdown. A stopped/missing container
does not authorize discarding retained workspace or history. Session deletion or
expiry requests cleanup, revokes the scoped device and cancels pending work before
Provider.Kill; the existing Worker serializes these lifecycle operations and drains
them before releasing its execution lease.

Keep the allocation after public Session deletion. Mark it released only after
owned compute/volume cleanup and evidence that its original Create has settled.
An unknown creation retains cleanup ownership even after an absence observation;
continue bounded scans for late resources without issuing another Create. This
conservative internal lifecycle does not define user-managed enrollment or prove
complete upstream expiry/error semantics.

Qualify the actual Docker/native sandbox before default cutover: real model
execution, file access, owned cancellation, restart with retained native history
and files, and rejection when required history is missing. Generated code and file
tools must not read daemon/model credentials, foreign Session history or another
tenant's workspace. Same-container placement, matching UID, mode bits, directory
bindings and capability flags do not prove isolation. Retain failed probes and
unverified limits; private functionality is not public hosted acceptance.

Before migration, archive existing edits and validation evidence. Reuse verified
authorization, resource/lifecycle ownership and safe filesystem primitives as
needed by V1. Stop work on separated-only enrollment, mirrored manifests and relay
mechanisms. Remove superseded unused code, configuration, tests, scripts and
task-owned temporary resources as each replacement is accepted. Preserve necessary
regressions, still-used official capabilities, product data and others' work.

The opt-in Codex deployment selector `PARSAR_CODEX_PERMISSION_PROFILE` chooses a
native named profile at harness startup and on both new/resumed threads, omitting
the legacy sandbox override. It is operator configuration, never a prompt option,
and rejects remote, none and temporary read preparations. Native managed
requirements own allowed profiles and deny-read enforcement. Keep the selector
unset for existing deployments. Managed native shells disable shell snapshots,
whose private files are inaccessible to tool execution; retain normal native shell
startup without granting tools access to harness state.
The [co-location qualification inputs](services/agents-api/deploy/codex/README.md)
record the pinned native/Docker prerequisites and limits; this switch alone does
not admit hosted Environments or authorize a workspace.

A managed Runtime's enabled/disabled network policy is immutable deployment input,
transferred through the provider-neutral bootstrap and checked against execution
preparation. The native adapter selects the corresponding managed profile; Core
and Docker do not select native profile names. New policy-aware peers advertise
`local_environment_network_policy`; enabled execution requires that capability.
The older explicit-disabled internal peer path remains supported without widening
its policy. Read-only workspace access does not require execution network policy.
A declaration alone does not qualify an image or admit public hosted creation.

A dedicated local Runtime uses one Environment-scoped device credential and an
immutable binding to that Environment's Session. It is excluded from general
device selection; another Session cannot claim it, including within the same
tenant. Deleting its Session invalidates credential lookup and heartbeat renewal.
Provision a new scoped device atomically rather than widening an existing shared
device credential. Revocation does not authorize silent placement replacement.

The private local Environment reference contains its identity and, for policy-aware
execution, its immutable network policy. Trusted Runtime deployment configuration
freezes the Environment, Session and workspace root;
requests cannot supply a replacement root. Local and remote references are mutually
exclusive. Use the same preparation/start lifecycle for native execution and the
existing bounded workspace controls for directory access. Local idle directory
reads use the existing filesystem helper directly, with no model credentials or
temporary harness. These private capabilities do not admit public hosted requests,
establish Provider lifecycle, or define the official `self_hosted` mapping.
Core rechecks the persisted Environment/device binding for preparation and active
reads; capability discovery cannot select or authorize a general device for this
placement. Local work uses the existing pending-input reservation and Worker
ownership without a remote connection resolver. The basic hosted profile supports `network.access: enabled` or `disabled`; the
actual image must qualify both native profiles before public deployment. Omitted
network settings mean enabled upstream and must not be silently treated as disabled.

Core and Runtime use common preparation, start, input-receipt, cancellation,
release and recovery semantics for Codex, Claude Code and MiniMax Code. Retain each
harness's native implementation behind its adapter. Core acts on verified capabilities and runtime
conditions; a capability declaration alone never grants public feature admission.
Extend existing interfaces during related functional work without introducing a
second framework or a broad rewrite. Codex, Claude Code and MiniMax Code have
qualified dedicated Docker and E2B V1 profiles. Each harness has equal standing;
qualify each image/template with the common full-loop acceptance before deploying.
Additional engines and hosted remote-executor separation remain separate work.
Later engines must satisfy the same applicable acceptance contract while keeping
their suitable native deployment layout.

Workspace reads may request the private `workspace_read_only` preparation profile
through the existing preparation factory and verified `workspace_read_preparation`
capability. It accepts only the bound Environment and resource identity; execution
options, model/MCP credentials, native Session continuation and model/tool input are excluded.
The Codex adapter creates temporary local state, reuses its native connection and
directory transport, and rejects Start. Its child inherits only process/transport
essentials. The private native read mode excludes system, managed, user and project
execution configuration and plugin startup while preserving native security
requirements. Ordinary execution keeps its stable state and configuration.
Reject the legacy mixed managed-config profile for reads rather than discarding
its enforced constraints together with execution settings.
For this read profile, `released` is published only after local Close succeeds;
cleanup errors retain ownership and report `cleanup_unconfirmed`. A failed factory
must return its resource with the error if cleanup remains unconfirmed; wrappers
must preserve both values. Successful cleanup retries publish confirmed release,
and stale status snapshots cannot publish success. Failed terminal status delivery
does not retry cleanup; ownership remains until an explicit release or shutdown retry.
A release request,
HTTP disconnect or remote socket closure alone is not cleanup confirmation. This
profile does not establish remote mutation quiescence or public Files admission.

Core directory reads reuse the Worker's Session scheduling reservation for idle
preparation and target the exact Run for active execution. Device selection uses
operation-specific capabilities; reading files never resolves model/MCP options
or creates a Turn. HTTP cancellation ends observation, not an admitted native read.
Keep the idle reservation through the bounded read and release attempt. Return
directory data only after confirmed Close; incomplete reads or uncertain cleanup
return unavailable without data. Release the Worker's scheduling reservation before
delivering the result so the caller can immediately request the next page.
Revoke the scoped read transport credential on
completion or failure. Runtime retains uncertain cleanup ownership and capacity;
this does not require a second durable Core owner registry or establish remote
write retirement. Public Files.list delegates workspace access to this reader;
the API owns tenant authorization, path validation and protocol pagination. Keep
partial directory coverage and unverified defaults explicit in the Files contract.

Source Files belong to the execution project and have an independent lifecycle
from copied workspace files. Store immutable source metadata and PostgreSQL large
objects in the execution database with the pinned pgx driver. Upload validation,
metadata insertion and bytes commit atomically; deletion removes metadata and
unlinks the object in one transaction. Keep OIDs private and authorize every
metadata/content/delete lookup by tenant before opening a body. Stream bounded
chunks; never hold an entire general Files upload in memory or use filenames as
filesystem paths. A read-only repeatable-read transaction preserves an admitted
source snapshot across concurrent deletion. Resolve that snapshot before entering
the existing Environment write path; deleting a source does not undo a completed
workspace copy. Bound request/transaction lifetimes, roll back incomplete bodies,
and never automatically retry ambiguous commits. Backups must include PostgreSQL
large objects; live deletion does not erase WAL or historical backups. Schema
rollback must not orphan existing source objects. Do not reuse product capability
tables or introduce a second destination writer for file_id.

Session Artifacts are immutable published output copies, separate from live
workspace files and general source Files. A private output exporter must reuse
the authorized workspace path boundary and stream bounded bytes. Require complete
capture and confirmed helper/transport success before publication; valid archive
syntax alone is insufficient. Never extract an output archive into Core's
filesystem or hold the global execution lease through a large transfer. Keep
publication ordered with Turn completion, and authorize stored reads independently
of Environment availability so published outputs can survive its expiration.
Capture bytes into private PostgreSQL large objects without a Session admission
lock; after confirmed export, lock and recheck the live Turn before staging metadata.
Before capture, seal native input under that lock using the private capture marker.
Later messages reuse the existing Environment input reservation and await the next
Turn; the public Turn stays in progress until publication settles. Directory reads
during capture use an independent authorized read-only preparation, not the released
native Run; they do not request model credentials or mutate the workspace. Cancellation
retains the existing Turn/reservation semantics. Do not introduce a second queue.
Publish metadata in the same transaction as Turn completion. Failed/cancelled Turns
discard private objects, and Session deletion removes both private and published
copies. Reuse the source-file snapshot reader pattern and common content response;
artifact deletion does not alter workspace files. Hosted execution requires the
Runtime's bounded output-export capability and exact read-only preparation binding;
capability advertisement alone does not qualify an operator's deployment.
Exporter component checks do not establish public Artifact compatibility.

Local inline file delivery uses the same authenticated daemon connection and exact
Environment/Session binding. The optional startup-owned `PARSAR_RUNTIME_WRITE_HELPER`
and `PARSAR_RUNTIME_STAGING` enable only the bounded installer primitive; they do
not grant public feature admission. Require a canonical executable outside the
Environment parent, canonical sibling workspace/staging directories on one mount,
and verified native tool denial of staging and its ancestors. Native credentials
and history remain outside that parent. Mode bits and path checks alone do not
qualify this layout. The read-only deployment needs neither writer setting.

Transfer a complete bounded body in acknowledged 64 KiB frames before invoking
the existing installer, verify the declared digest, and run no model for upload.
Keep the existing private 50 MiB bound distinct from upstream protocol limits.
The dedicated Runtime excludes execution while receiving or applying a write;
malformed, incomplete or expired transfers cannot reach the installer. Exact
commit/rejection receipts release the mutation owner. Missing or ambiguous
receipts retain uncertainty; observer cancellation and local process exit cannot
prove non-mutation. Before public admission, Core must durably reserve the write
under the Session lock and prevent successor mutation across restart until exact
settlement. Do not replay the request or introduce general replacement machinery.
Read-only operations retain their own authority and bounded ownership requirements.

Keep prerequisites specific to the public operation being implemented. Native
harnesses execute; adapters translate protocols and fill demonstrated capability
gaps; Core owns public semantics, authorization and resources. Before adding a
mechanism, identify the current operation it enables and why existing native
capabilities or interfaces do not suffice. Durable metadata queries need no live
runtime. Live file reads require an authorized, isolated view of the exact workspace
and bounded operation ownership, but not a complete file-write, environment
replacement or placement-retirement implementation. Apply mutation fencing and
retirement guarantees where an operation can write, replace or retire that owner.
Read-only access still requires tenant/resource checks, path isolation and safe
failure when the authorized workspace cannot be reached; it never grants public
admission merely because an adapter advertises a capability.

Use distinct authorization for callers, devices and environment connections. A
co-located harness must not expose broader application credentials or other tenants'
secrets to generated code. Directory bindings and process identities do not provide
filesystem isolation. Preserve or demonstrably restore native history across
compute replacement; never silently move a bound Session or replay unknown work.
Self-hosted compute/files remain caller-owned, with explicit cleanup separate from
Session deletion. The full Environment implementation remains pending; follow the
[pinned contract and acceptance sequence](contracts/agents-api/environments.md)
and the [two-engine placement prerequisites](contracts/agents-api/workspace-placement.md).
For co-location, qualify both deployment isolation and native tool restrictions.
Bash sandbox settings alone do not establish file-tool or whole-harness isolation.
Never enable an environment profile before those boundaries are verified.

The internal Store creates one Environment with an environment-bearing Session in
its creation transaction. The Session upsert selects the retry winner; retries
never create or repair associations. Environment identity/state live in their own
table. Tenant ownership and immutable configuration come from the owning Session,
without duplicated JSON, tenant columns or generated IDs in the creation hash.
Environment reads join that Session and exclude deleted Sessions; deletion retains
ownership for later settlement/cleanup. Existing `none` and legacy missing
configuration create no Environment, and historical internal snapshots are not
backfilled. Creation and recorded-intent retry snapshots load the Environment with
the Session row/cursor in the same transaction, without borrowing subsequent
activity or Turn state. Initial state is `pending`; authenticated connection observations follow
the lifecycle rules below.
Public creation supports a `self_hosted` Session on the Codex profile when
execution and a validated executor origin are configured. Require an absolute
POSIX workspace directory without NUL, CR, LF or backslash for the current adapter;
omitted/null capability directories use the empty default.
Supported non-deferred function tools use the existing validation and native
callback bridge. Nonempty capability directories and other engine placements
remain rejected implementation gaps. Session output uses the owned
Environment association; file operations and populated installation metadata remain separate.

Environment retrieval uses the existing tenant-scoped join to a live owning Session
and its durable connection status, independently of execution or registry setup.
It preserves project-shared read access and exposes only the pinned resource fields.
The current closed self-hosted configuration has no API-managed file, plugin or skill
installations, so those required arrays are empty. They are not a filesystem listing
or a claim about native discovery. Reuse the strict Environment configuration parser
and reject unsupported installation fields/capabilities or resource states instead
of treating unknown inventory as empty. No read initiates native work or changes
connection state; connection does not establish readiness or process quiescence.


The private Environment input reservation stores one canonical message batch before
Turn admission, with a five-minute deadline from the database clock. It requires
an Environment-bearing Session without active work. Reservation and direct input
paths share the Session lock and retry identity; pending or settled keys cannot
bypass the reservation through direct admission. A pending reservation blocks new
direct batches, including cancellation, while successful earlier retries remain
readable. Promotion commits the original inputs, history, reservation settlement
and execution claim (`queued` to `in_progress`) together; expiration and targeted
cancellation retain the terminal identity. Session deletion
cancels pending input in the same transaction. A terminal reservation retry must not
affect a later reservation or Turn. Evaluate deadlines after acquiring the Session
lock, and return terminal storage outcomes without rolling their transaction back.

Initial messages for a newly created Environment-bearing Session use that same
reservation in the creation transaction, including its connection-action event.
The creation winner alone inserts it; the original pre-work snapshot and stream
cursor remain unchanged. A durable initial/later flag defaults historical rows to
later input without inferring origin. Initial expiry projects a failed Session and
safe error before any Turn exists; later expiry retains idle semantics. Failure
events capture the settled activity and Usage atomically. Late connections and
creation retries cannot reset or replay expired input, and newer work supersedes
old activity without changing its event snapshots. The Environment itself is not
failed by an input deadline. This Store rule covers actual self-hosted and internal
hosted associations; it does not enable hosted providers. None/absent Environment initial input retains
immediate Turn admission. Cancellation/deletion keep their existing semantics.

Ordinary and streamed public self-hosted creation accept initial text through this
transaction after configuration and new-work lease checks. They return the owned
Environment ID and executor URL while offline, without waiting for admission.
The creation stream sends its original pre-work `created` snapshot before the
committed connection action. A disconnected observer leaves committed input intact;
only the existing Worker prepares, promotes and starts it. Saved-Agent retries with recorded intent
recover before fresh execution admission or source resolution; inline retries keep
their existing resolved-snapshot validation.
Later live subscribers observe only future events and recover history through queries.

The public self-hosted input profile accepts message-only batches. Under the same
Session lock, recover the original reservation or direct receipt before choosing
current active input or idle reservation. Active messages use existing ordered input
receipts without a new Turn, preparation or reservation; idle messages retain the
readiness and promotion path, including already-connected environments. Pending
reservations keep their gate and deadline. An unlocked activity read or retry after
a conflict must never choose a different admission path. Mixed inputs remain gaps;
these restrictions do not narrow the pinned protocol target. Cancellation-only batches
use the existing direct admission after configuration and execution-ownership checks.
Only the Session-locked transaction chooses the active Turn or an idle receipt;
matching retries retain that target even during later work. Cancellation cannot
create a Turn or bypass preparation. A new cancellation still conflicts with a
pending reservation; it does not cancel pre-Turn input. Its 204 response confirms
durable admission, not native completion or process exit. Homogeneous function-result
batches also use direct admission after those same checks: their explicit Turn/call
identity selects an existing pending call, never new work. Reuse function validation,
Session-locked whole-batch receipts, preserved output/error fields and native
application acknowledgements. Matching retries remain bound to their original calls
after completion or during later work; new results cannot bypass a pending reservation.
Definitions remain fixed through preparation and cold native continuation. These
callbacks are not installed Environment metadata. Mixed result/cancel publication
and exact hosted action-removal timing remain separate gaps.
Promotion requires the current leased execution writer and the caller's
retained native preparation; never hold a database lock during external preparation. Only
the first successful non-replay receipts authorize Start on that same preparation.
An admitted retry returns the original receipts without reclaiming execution; a
read or uncertain commit never authorizes another Start. A crash after promotion
but before Start uses existing claimed-Turn reconciliation (`execution_interrupted`),
including unbound or deleted Sessions, rather than ordinary queued dispatch. Deletion
after claim requests cancellation under existing active-Turn semantics.
The Worker expires at most 32 due reservations on each existing tick, after
checking ownership and before checking devices or execution slots. The sweep
requires the leased Store and uses its connection with the existing transaction
timeout; it never falls back to a pooled writer. A partial deadline index and
Session row locks with SKIP LOCKED let unrelated work proceed around contention.
The candidate cutoff is statement time; settlement rechecks the database clock
after acquiring the Session lock. This bounds mutations and transaction time, not
the number of examined locked rows. Restart resumes expiry on normal ticks without
a separate scheduler or backlog-draining loop. No failed Turn may stand in for a
pre-Turn connection failure.

Session activity before a Turn is derived from the latest relevant reservation and
authenticated connection state. Offline input requests `environment_connection`;
connection arrival clears that action to `idle`, while the prepared Worker still
owns native readiness and admission. An idle offline Environment alone requests no
connection. Reservation/connection changes commit immutable Session activity and
usage snapshots in the same transaction; SSE must not substitute a later Turn or
action set. A newer or active Turn owns subsequent activity. Settled non-initial
reservations clear their action to `idle` until newer work exists, even after an
earlier failed Turn. This local settlement policy does not establish hosted expiry
errors or initial-input asynchronous failure semantics; those remain unverified.

Session GET/list/metadata responses and live SSE share the safe `self_hosted`
output projection. Its `remote_url` comes only from the executor registry's
validated configured origin, never request headers or a daemon address. Include
the owned Environment ID, workspace and capability directories without exposing
private configuration. The standalone Environment resource remains separate.
Acceptance must pass that exact URL and ID to the
caller-started executor and observe real remote execution through the existing
Worker, daemon and harness, with fixed SDK and raw HTTP/SSE checks.

A self-hosted input HTTP request returns 204 only after durable admission. Its
wait uses bounded pooled operations, outside transactions and execution lease
ownership; it cannot prepare or start native work. Only that route extends its
response write deadline to six minutes for the original five-minute database
admission deadline plus response grace. Request/observer disconnect stops waiting,
not the durable reservation or execution; retries keep the original identity and
deadline. The Worker remains the readiness, promotion and Start owner. Local failure
mapping uses 409 `environment_input_expired` / `environment_input_cancelled`, 503
`execution_unavailable` for ownership loss, and existing 404 for deletion. Exact
hosted failure status/body and pending-input crash recovery remain unverified.
Principal acceptance must use public Session creation and input against the built
standalone service, including a wait exceeding its ordinary 30-second write timeout,
real remote commands/files and a second native-history Turn. Private provisioning
or injected API handlers cannot substitute for that workflow.


The opt-in native Codex executor registry lives in
`services/agents-api/internal/executor/codex`, outside public API handlers and the
daemon device gateway. It reuses the worker's execution lease and Store ownership
reads. The operator issues connect-only executor keys for a complete typed principal
within an already verified project-to-tenant mapping. A key has a stable explicit
management UUID, immutable principal and optional exact-Environment restriction;
a principal key needs no Session at issuance. Only its digest, creation/issuance
times and revocation state are persisted. Ordinary issuance never replaces an ID;
rotation and revocation require that ID and full principal. Exact-target issuance
and rotation share the Session deletion lock and require its recorded creator.

Every authorization checks the current digest, non-revocation, project partition,
Session creator kind/ID, optional restriction and live Session in one database
snapshot. Unknown historical creators cannot authorize an executor. Deleting one
Session denies that target without revoking a principal key serving other Sessions.
Keys have no connection-ticket expiry; their validity ends through explicit
rotation/revocation, while each target remains subject to current ownership checks.
Keep caller, device, harness and executor credentials independent; no raw-token
import or read-back is provided. Never log registry bearer or URL capabilities.

Migration 26 retains legacy key digests/restrictions under their Environment UUIDs
but revokes them with unknown principals. Do not infer historical identities or
project mappings. Stop older registry and operator writers before migration;
deploy the issuer, registry and launcher together, explicitly reissue keys and
restart executors. Reserved legacy IDs cannot be claimed or rotated into principal
keys. Downgrade cannot discard new principal-key identities or undo revocation.

Registration IDs, five-minute connection capabilities and socket generations are
process-local. Re-registration replaces the current socket; late close callbacks
cannot clear its successor. Restart invalidates old URLs and requires registration
again. An executor retaining a valid credential may register again: permanently
excluding it requires key revocation/rotation. The current executor digest is rechecked for registration, validation, socket
attachment and live socket heartbeats; rotation/revocation applies without restart.
Registration replacement orders credential observations so an older request cannot
overwrite a newer credential's registration. Heartbeats run every five seconds
with a four-second authorization budget; closing sockets is an observation bound,
not immediate revocation of remote side effects. Ownership is also rechecked; failed execution ownership closes the registry. This
is bounded connection observation, not a guarantee of native process quiescence.
Durable connection state and immutable Environment event snapshots follow the
leased observation path below; a socket never establishes harness readiness. The harness registry grants a distinct, exact-Environment credential access to
native `/connect`; the executor alone calls `/validate`. Each connection URL and
one-use key authorization are separate five-minute capabilities bound to the
current registration, executor socket and complete harness public key. Grants are
bounded; refresh may issue unused grants without disturbing an active pair.

Internal execution owners obtain random harness credentials from the native
registry after current lease and exact tenant/Environment authorization. The
registry retains at most 32 credential digests in memory. The owner context spans
preparation and its transferred Run; release, owner cancellation and registry
shutdown invalidate that credential, its pending grants and its own connected pair.
Recheck the same live credential under the registry lock after authorization
queries in connect, attach and validation. Old cleanup cannot revoke a successor.
The five-minute connection-ticket lifetime does not expire an active execution
owner or impose a Turn deadline. Pair closure is not proof of OS quiescence.
Static harness-key files are retired explicitly, without a fallback or public
issuance endpoint. Executor authorization also requires the recorded Session
creator; tenant ownership alone cannot authorize executor connections. No credential bearer belongs in snapshots, events, logs or the database.

One independent harness connection pairs with each executor connection. Native
binary messages pass unchanged, up to the pinned 256 KiB limit, with one data
writer and one in-flight message per direction. Write deadlines bound stalled
peers. Either peer disconnecting closes both physical sockets and invalidates the
pair's grants; this lets native Session/process recovery run in the executor.
Never forward queued ciphertext to a replacement or invent transport replay.
Concurrent native commands and files share one connection; additional independent
harnesses are rejected without eviction. Full public Environment conformance and other engine
placements remain separate work. Native transport annotations are excluded from the
pinned public SDK OpenAPI output; their routes are documented in the service guide.

Remote file operations must share the native execution owner's filesystem and
authorized connection. The pinned stock app-server `fs/*` methods select its local
Environment and cannot access an executor-only workspace. The opt-in
[shared-filesystem probe](services/agents-api/tests/native/README.md#shared-native-filesystem-owner)
instead injects one upstream `EnvironmentManager` into the native in-process
app-server and uses its typed filesystem directly. This is a prerequisite
experiment, not a production daemon selection or public file implementation.
The pinned in-process transport can silently drop notifications under saturation;
absence of a `Lagged` event does not prove lossless delivery. Resolve that event
contract and process/authorization ownership before adopting an embedded runtime.
Public workspace paths, file references, live metadata and pagination require
separate protocol acceptance; no model prompt or shell command implements file IO.

The opt-in [raw manager qualification](services/agents-api/tests/native/raw_manager/README.md)
tracks an explicit patch against the same native pin. It publishes the raw runner's
stock-built manager through an additive entrypoint, retaining native configuration,
processor and transport assembly. The ordinary runner remains unchanged. Handle
publication is not readiness or revocation; its owner must supervise runner failure,
gate operations on initialization/readiness and release retained handles on teardown.
This is a private native dependency experiment, not a production runtime selection.
Record the patch, build overlay and artifact identities separately from upstream.
Production adoption requires real acceptance of the requested operations against
the remote workspace/history, bounded ownership and caller authorization. Require
stale-write fencing when admitting mutations or replacing their owner, rather than
making it a prerequisite for every read. Connection observation generations alone
cannot retract already-issued filesystem mutations.

The opt-in [private harness artifact](packages/codex-harness/README.md) consumes
that same hook in a separately named executable at the unchanged native pin.
Its canonical patch lives in the package; qualification manifests reference the
same bytes. Export the exact upstream commit, verify the lock normalization and
named-binary overlay, and retain source/toolchain/artifact provenance. Do not build
from a mutable upstream worktree or present this integration as a stock binary.
Capture operator selectors before native bootstrap; retain native dotenv/helper
initialization before threads and its alias guard until runtime teardown.
The existing Go RPC owns its raw stdio child. A private same-user local socket
offers metadata and bounded reads through that runner's manager, with a frozen registry
Environment UUID, the adapter's native `remote` manager key, and no local fallback.
Keep socket admission bounded and stop it when the runner ends. Caller disconnect
only stops response delivery. Runner completion stops pending frames/new admission
and drains the already admitted operation within its original deadline before local
release; an unresolved drain remains an owner failure. This retains a native wait,
not a remote retirement guarantee. External forced child exit can interrupt the
drain; the existing daemon RPC's short grace/local-reap contract must be reconciled
before a file consumer can infer settlement from release. An unresolved native
file timeout must stop the owner before admitting another operation; client
frame/response timeouts are connection-local. Never equate dropping the native
response future with remote settlement. Bound Tokio runtime shutdown so an
uncancellable native stdin read cannot hide local process exit from the RPC owner.
The separately hashed bounded-read hook pins one existing native RPC connection
for open, sequential block reads and acknowledged close. It retains the pinned
native wire and stock stream behavior. Bound returned bytes and use one-byte
lookahead for exact/truncated results; do not promise a file snapshot. Uncertain
open/read results remain uncertain even if a later close replies. Unconfirmed
close fails the owner, with no partial success or connection replacement retry.
These are private adapter outcomes, not new official Files fields or error semantics.
The socket directory
must be new and private under `~/.parsar`; native/helper/socket selectors remain
operator configuration. `PARSAR_CODEX_HARNESS_BIN` opts the native Codex adapter
into this artifact for validated remote preparations only; stock helper discovery
and all nonremote execution remain unchanged. The adapter derives each private
Environment/workspace binding and owns a short IPC directory under canonical
`~/.parsar`, independently of deeper `PARSAR_HOME` profiles. Reuse the existing
Prepared-to-Session transfer and RPC child; remove IPC only after that same child
has been reaped, including initialization failure and Close timeouts. No wrapper,
new capability, public Files admission or default daemon selection is introduced. Private file path checks do not qualify filesystem isolation, idle
ownership, remote retirement, or the existing RPC's full backpressure behavior.
Public cancellation qualification for this artifact reuses the fixed SDK/raw HTTP
fixture with a task-isolated native system configuration and independently observed
owners. Preserve existing behavioral assertions and keep that test placement
separate from production isolation or public Files admission; see the
[native acceptance guide](services/agents-api/tests/native/README.md#public-cancellation-with-the-optional-harness).

The private daemon `workspace_read` control targets an existing preparation handle
or its transferred active Run on the same authenticated device connection. Require
the exact frozen Environment identity; callers cannot supply sockets, credentials
or workspace roots. Shared routing uses the optional `agent.WorkspaceReader`
interface, without selecting an engine by name. The optional Codex artifact uses
its existing same-manager socket; stock Codex and other adapters remain unsupported.
The same control accepts `operation: directory` through the optional
`agent.WorkspaceDirectoryLister`, with mutually exclusive byte/entry limits and
typed directory metadata. Directory responses carry at most 1024 single-component
UTF-8 names of at most 255 bytes, so escaped metadata stays below the existing
frame bound. These are private transport limits, not public Files parameters.
Byte and directory operations share target checks, correlation, capacity and
retained operation waits; neither creates a Run or selects an engine by name.
Current bound preparation admission requires `RemoteEnvironment`; the qualified
co-located Claude workspace profile does not accept that placement. Its private
directory capability therefore does not establish end-to-end control admission.
Integrate its verified workspace identity through the existing lifecycle before
claiming Claude control/public Files acceptance; never fabricate remote bindings.
This control is not a public Files endpoint or capability advertisement.

The separate optional `agent.WorkspaceDirectoryLister` observes one workspace-relative
directory on the existing Prepared/Session owner; empty path selects its root.
Return single-component names, entry kind, regular-file byte size and explicit
truncation only after directory/metadata access and handle cleanup settle. Reuse
byte-read admission, uncertainty and caller-detach ownership where applicable.
Do not promise a snapshot, recursive traversal or public pagination through this
private interface. Codex uses the same manager's native process backend to invoke
an operator-installed `agents-api-codex-directory` through explicit argv, without a
shell. `PARSAR_CODEX_DIRECTORY_HELPER` selects the executor-side executable and is
frozen in the private child binding; an absent or invalid selector rejects only
this operation. Keep that qualified installation outside the writable workspace.
Require the verified Linux sandbox, read-only workspace/helper runtime access and
restricted network. The helper anchors all traversal to no-follow descriptors and
bounds enumeration before collecting names. Accept only a complete versioned result
after native exit/output close. Account for native output and terminal event
sequence numbers: retained-output eviction or a capped response's `closed` flag
cannot establish completeness. Reject missing/oversized/invalid output; terminate
and confirm exit/output close when rejection precedes settlement. Keep the existing
retained wait and owner failure on native uncertainty; never retry unknown work.
Private directory support alone does not enable a
daemon control operation, public Files route or capability advertisement.

Bound encoded request payloads to 8 KiB and correlation IDs to 128 bytes before
admission. Do not echo oversized IDs; omit oversized trace metadata in replies.
Bound raw control results to 1 MiB within the existing 4 MiB transport frame; the
native hook's separate 8 MiB bound is unchanged. Neither is a pinned public protocol
limit. Successful reads require complete bytes/truncation and acknowledged native
close. Safe native rejections carry no bytes; interrupted or ambiguous reads remain
unknown and stop further reads on that owner. Local RPC reap never establishes file
settlement. Retain a dispatched read's original bounded waiter across observer
cancellation and resource transfer/release; stop new admission on resource closure.
The gateway bounds subscriptions and never retries or replays on reconnect. Duplicate
pending operation IDs cannot start another read; this control does not promise durable
idempotency or result recovery. Preparation/Run ownership, public path authorization,
remote retirement and future Claude placement retain their separate requirements.

The private [raw Files composition](services/agents-api/tests/native/raw_files/README.md)
reuses the pinned native socket client and the same typed Files/registry fixture.
Record its fixture-only workspace dependency patch separately from the manager
hook and third-party versions. Its finite real Files/history workflow does not
qualify the client's internal unbounded event queue for production. Client closure
is not runner shutdown; join the stock runner before reporting owner teardown.
Bounded native stream checks retain the original whole-file assertions. A truncated
read's asynchronous close and stream EOF are not operation-retirement receipts;
keep production caller-detachment and path-admission qualification separate.
Its dedicated cancellation scenario distinguishes native interruption from command
retirement. Target native background termination only by observed current-Turn
item/process identity, and verify Files plus retained interrupted history through
the maintained native client before any production ownership or public admission.

The optional Codex file installer runs through the existing native process interface
with bounded stdin chunks, declared length and a SHA-256 commit trailer. It fills
the demonstrated native hard-link overwrite and whole-message size gaps; it is
not a second filesystem service or public admission. Reuse held-directory traversal
and existing rustix directory-relative operations for replacement. Keep preparation, caller
authorization and uncertain mutation recovery in their existing owning layers.
Require an existing disjoint staging directory on the destination filesystem.
The operator must protect that directory and its ancestors from native tool and
background-process writes; same-user mode bits alone do not do so. Use separate
native filesystem policies for the installer and workspace tools, with a dedicated
staging directory per Environment. The qualified installer sees one writable parent
containing only that Environment's workspace and staging; tools retain workspace-only
write access. Keep history, credentials and other tenants outside that parent.
Separate sandbox bind mounts may reject rename even on the same backing filesystem;
never fall back to copying. The helper cannot attest that placement rule;
public admission must establish it. Concurrent workspace writers need not be
globally stopped to protect staged bytes. Temporary-file cleanup is best effort.
A queued stdin receipt, missing helper result or process termination is not a file
commit receipt. See the [installer contract](packages/codex-executor/README.md#scoped-file-installer)
for private limits, cleanup, metadata and concurrency semantics.

The private harness file socket admits writes only with a frozen operator helper
and staging binding; read-only preparation removes that binding. Workspace and
staging must be distinct siblings under one non-root Environment parent, and the
helper must be outside that writable parent. Receive the complete bounded body
before starting a native process. Transfer 64 KiB chunks plus the digest through
one captured native process; require Linux sandboxing and an exact versioned
commit result with successful exit and complete output closure. Native queued
stdin is not a commit. Caller detach does not cancel admitted work; uncertain
input, output or deadline stops the existing owner without replay or replacement
claims. Public Files.create, trusted placement admission and durable mutation
recovery remain separate work. See the [private transport contract](packages/codex-harness/README.md#private-file-writes).

The private [retirement qualification](services/agents-api/tests/native/retirement/README.md)
separates native connection/processor shutdown from already admitted filesystem
work. Its hashed test-only scheduling overlay is not a production native patch.
Do not admit a replacement writer based only on socket closure, task cancellation,
command exit or a Core lease change. Require an actual executor mutation-drain or
enforced placement-retirement boundary; retain uncertainty across recovery when
that boundary cannot be established. Native source evidence, instrumented mechanism
tests and uninstrumented real execution remain distinct acceptance claims.
The opt-in whole-placement fixture uses an exact task-owned Docker instance and
cgroup/process observations before successor writes. This is a local-filesystem
qualification, not an authenticated remote retirement receipt or public admission.

The explicit `parsar-daemon placement enroll/retire` operator commands own the
first local Runtime retirement consumer in `internal/agentdaemon/placement`.
They use a fixed local Docker socket, an exact labeled container/incarnation,
private durable state under `~/.parsar/placements`, and a per-target process lock.
Enrollment is limited to the documented unprivileged Linux/cgroup-v2 local-storage
profile. Keep controller state and the canonical Docker socket outside generated-code mounts,
including when a workspace source is a filesystem root. Every source must reside
on a whole-filesystem host mount whose device appears exactly once in the controller
mount namespace; bind aliases, subvolume roots, stacked mounts and missing mount
evidence are unqualified. This bounded profile does not resolve arbitrary mount graphs.
Stopping, independent membership/process observations and non-forced removal must
precede a durable successful receipt. Recovered receipts must complete their directory-sync barrier before success.
Missing evidence or a crash after removal
but before receipt persistence remains unknown; never clear it based on absence.
Optional `--environment` enrollment freezes one canonical Environment UUID in a
version-2 local receipt. Every scoped retire/reconcile must match it before any
supervisor access or completed-receipt recovery; omission cannot bypass the check.
Version-1 unscoped records remain unscoped and cannot be adopted by a scoped retry.
Older controllers reject version-2 records. Enrollment is trusted operator consent,
not verification of Core resource existence or tenant ownership; the future Core
consumer must validate those using its existing authenticated associations.
Normal harness release is unchanged. This local operator command is not Core
admission, authenticated remote receipt support, or public Files compatibility.
See the retirement fixture README for the profile and explicit native acceptance.

Connection observations use the existing execution lease and Session lock. A
separate `environment_connections` row retains the current generation and revision;
`environments.status` and its Session Environment-event snapshot commit together.
The producer serializes replacements, then numbers socket observations within each
generation. Duplicate or older revisions and superseded generations are inert.
Replacement retires a previously connected observation before publishing its new
registration. Registration alone creates no connected event. Event payloads contain
only public Environment identity/type/status and nullable error, never configuration,
credentials, registration IDs or revisions. Transport observations have no asserted
Turn association. `connected`/`disconnected` are distinct from native preparation
readiness; do not cast resource `expired` into the event vocabulary or emit `ready`
for a self-hosted connection.

Registry writes run synchronously outside the relay mutex, with a four-second
operation budget independent of client disconnect. Shutdown closes sockets, shares
one four-second budget across captured disconnects, and drains accepted writes
before the Worker releases its lease. Missing/deleted/terminal targets retire only
the matching connection; other write failures are logged, retained by
`LifecycleError`, and close the registry until restart. No successful persistence
or continuous connectivity is inferred after a failed write. Before starting
connection producers, a new Worker clears old generations and records disconnected
state for old connected observations in batches of 32. Deleted resources stay
hidden. Stop old writers before migrating/deploying this lifecycle; downgrade
refuses to discard retained generation fencing. Metadata reads and full lifecycle conformance remain separate requirements.

The optional [Codex executor launcher](packages/codex-executor/README.md) is a
separate Cargo package. Pin its native git revisions, transport patches, toolchain
and lock; use the upstream executor/auth/runtime APIs without changing stock CLI
credential protection. It reads only an explicit principal-key credential file with an optional exact-Environment restriction
and uses the matching installed native binary/resources for hidden filesystem and
sandbox helper modes. Keep its state below `~/.parsar/`; do not load ambient
OpenAI login credentials. HTTPS certificate/hostname verification remains enabled.
The separately named command does not establish stock-command compatibility or
enable public Environment admission. Linux x86_64 is its initial deployment target.

The executor build also provides a small `agents-api-codex-directory` helper for
bounded, descriptor-scoped directory observations where the pinned native walk
cannot maintain path isolation during concurrent ancestor replacement. Use the
existing native process API, explicit argv and a qualified read-only sandbox;
keep the executable at a trusted operator path outside the writable workspace.
The adapter supplies its frozen workspace root. Enumerate and stat using retained
no-follow directory descriptors, bound scanning before collecting all names, and
require complete output plus native exit/close settlement. This is a private
adapter prerequisite, not a new public protocol, transport or filesystem framework.
The helper alone grants no tenant authority, public Files admission, snapshot or
workspace-replacement guarantee. Preserve the stock executor/model loop.

Native app-server placement is a prerequisite to typed dispatch. The pinned Codex
app-server accepts registry configuration at startup; use explicit native Environment
selections for the first thread and every Turn. Resume does not restore selections
from history. Keep its process cwd and persistent `CODEX_HOME` local, separately
from the executor cwd. Readiness and observed tool/file results are required:
a completed native Turn alone does not establish successful remote execution.
Apply an intentional native shell environment policy; upstream defaults do not
filter all credential variables. Filtering is not process or filesystem isolation.
The opt-in [placement probe](services/agents-api/tests/native/README.md) documents
its real-provider prerequisites and limits. It does not enable public admission.

The private daemon `remote_environment` descriptor carries Environment identity,
executor workspace and transient native connection URL/token. `WorkDir` and
`CODEX_HOME` remain harness-local. The selected adapter owns the connection
protocol; Codex Noise configuration and native Environment selectors never enter
the API core. Do not persist the connection token in configuration, events or
completion metadata. Existing private provider configuration and device profiles
have separate credential ownership.

The initial Codex adapter advertises this mode only for the verified 0.153.4
protocol, propagating the capability through the real heartbeat/gateway. It checks
native remote readiness and absence of local fallback before starting a thread.
Supply a stable state key, strict resume and completion release: each prompt owns
one harness, and a bound Environment permits one harness connection. Send the
native selection on first thread creation and every Turn; cold resume does not
restore it. Reject conflicting native transport settings, unsupported engines or
versions, non-POSIX executor paths, and local managed Skills/MCP/plugins/authoring
or attachments. Apply explicit core shell inheritance and credential exclusions.

Codex preparation initializes the existing RPC child and verifies environment
readiness without creating a native thread or starting model work. It carries no
RunID or prompt; the existing Factory resolves its state key before using the same
Prepare/Start implementation. The resolved plan, tools and resume identity are
fixed during preparation. Start accepts the actual RunID, prompt and output sink,
checks remote status on the retained RPC without reconnecting, and transfers that
resource once to the normal Session. Failed start or abandoned preparation closes
the child and cleans temporary plan resources; deferred preparation Close is inert
after transfer. The owner context spans the whole harness lifetime, while startup
operation deadlines remain separate. Owner cancellation/RPC exit release pending
resources; executor loss is checked at Start, not continuously monitored. This
adapter seam does not provide public admission or a new scheduler.

Every executable preparation must implement `PreparedCancellation`; read-only
preparations may implement only `Prepared`. The Router rejects and closes an
executable preparation before `ready` when that contract is missing. Codex fences
future Start under the transfer lock; unused resources use preparation teardown,
while transferred resources use the existing Session cancellation path inside the
adapter. `Close` remains inert after transfer.
`CancellationOutcome` exposes observed content, Usage and verified native identity;
an unstarted resource has no measured Usage or observed resume identity. This is
best-effort cancellation and an observed snapshot, not immutable final output,
notification drain, caller-deadline compliance or remote process quiescence.
From Start admission onward, the exact `PreparedCancellation` object is the sole
release target: a late Session never receives fallback `Cancel`, and the Router
never falls back to preparation `Close` or owner-context cancellation. Successful
`Cancel` means local cleanup is complete and no more output writes can occur. A
failed or timed-out call returns without waiting for output, retains execution
ownership, and permits only a later explicit serialized retry on that same object.
Before publication, failed cleanup also retains the preparation slot. Successful
publication permanently transfers resource tracking to the Run; subsequent release
does not restore preparation ownership or make its old handle cancel that Run.
Forwarded permission and user-choice observations from that cancelled handoff do
not register actionable interactions. Codex prepared cancellation also waits for
the transferred Session's local cleanup, which can finish after output closes;
ordinary Session cancellation retains its existing behavior.
One prepared-output consumer starts before `Prepared.Start`, so native output beyond
the 64-frame channel capacity cannot deadlock Start. It retains the first terminal
frame, drains later output, and forwards accepted frames before the observed cancellation
outcome receipt. Missing capability, failed cancellation or failed forwarding cannot
produce an applied receipt. An unused resource may supply an empty observed outcome;
the Router never fabricates one.

The returned Session remains private until the `started` status send succeeds. The
handoff has one permanent release claim, one current native attempt and one
success-only settlement. Function results, permission decisions, user-choice
decisions and steering admitted before the claim hold the same operation barrier
through native submission, replay bookkeeping and receipt delivery. Natural
completion waits for Start publication before cancellation; an abort wakes that
same attempt and may fence a concurrent Start. The initial started-status send is
bounded by the preparation deadline, which is rechecked at publication commit.
Delayed expiry callbacks cannot cancel a successfully published handoff. The successful attempt waits for Start and the sole output
consumer, then forwards the retained terminal frame and removes the Run. Internal
paths join the current attempt; only an explicit cancel, Release, expiry, device
shutdown or later Router Shutdown may retry a failed attempt. Workspace reads require
a published, open Session but keep their independent bounded lifecycle.

Receipt settlement waits at most ten seconds, with a separate five-second send
budget and the existing gateway settlement deadline. A timeout does not free the
resource or stop tracking late cleanup. Shutdown cancels receipt waits while owned
Start/cleanup work remains tracked. This ordering covers cancellation received while
Start is pending; ordinary post-transfer cancellation, complete native output and
remote process exit retain their separate limitations.

The private daemon preparation controls reuse execution configuration but reject
input, RunID, Conversation, attachments and product authoring. The initial profile
requires a remote environment, stable state key, strict resume and completion
release. Its separate capability is registered through an execution-only factory
and preserved through the product registry wrapper and heartbeat mapping. Native
details remain inside the adapter; this private profile does not narrow upstream.

Preparation request IDs correlate only control responses. The daemon returns a
fresh opaque handle before slow work; Start supplies that handle and the actual
RunID/prompt. Handles belong to one daemon connection. Gateway preparation
subscriptions do not register Runs. Per-handle revisions order asynchronous status
snapshots; reject responses describe control errors without inventing run events.
At most four native preparations may be preparing, ready, starting or closing.
Start admission expires five minutes after acceptance; retries do not extend it.
Failed cleanup retains the native resource and its capacity until Close succeeds;
terminal handles with retained resources cannot be pruned or started again.
At most 64 request records are retained; retired request IDs may allocate a fresh
handle, while old handles cannot consume replacements. This is not durable
exactly-once preparation or cross-connection recovery.

Preparation and Start execute outside the receive loop and router lock, with
tracked lifetime work. Start reserves the real RunID and fixed cancellation target
before native work; cancellation in that phase uses the adapter contract across
the transfer. A late result cannot resurrect released ownership. Shutdown claims
or retries every prepared release under the lock before closing its cancellation
signal. A failed native attempt returns Shutdown without waiting on an output
consumer that may still be blocked; a later Shutdown retries the same target.
Successful publication stops the preparation deadline and uses the same prepared
output consumer and completion release;
later preparation Release cannot cancel that Run. Released/expired status makes
the handle unusable; asynchronous native cleanup still counts toward capacity and
does not promise immediate OS quiescence. Release retries retained cleanup.
Concurrent Shutdown calls join one tracked attempt within their caller deadlines;
a later call retries failed preparation cleanup and reports any remaining error.
A caller timeout does not discard ownership or repeat in-flight cleanup. These
records remain connection-local, not a persistent remote retirement fence.
The public idle-text path uses this
admission/start wiring; complete Environment lifecycle remains required work.

This daemon slice keeps existing best-effort cancellation and harness cleanup.
Native detached-session cleanup may stop remote commands after a delay; an applied
receipt is not immediate process quiescence or complete final output/Usage. The
opt-in registered-daemon test independently observes PID exit and stopped heartbeats
while the daemon, registry and executor stay alive. Targeted process termination,
cross-Turn background preservation and complete public cancellation/lifecycle remain
separate work. The public idle-text profile has its own built-service acceptance;
an adapter probe alone does not establish public compatibility.

The private Dispatcher can execute a pending Environment input on an already bound,
capable daemon. It requires the current leased Store before resolving transient
connection credentials. A typed callback supplies only the URL, token and release;
native registry types remain outside execution code. Derive Environment identity
and workspace from Store ownership. Retain the same physical peer and preparation
handle through readiness, atomic promotion/claim and the first non-replay Start.
Initial prompt/cursor come from the reserved batch and its receipts; later messages
use ordinary steering. Never hold a database lock during native preparation.

Observe the original pending deadline, cancellation, deletion and peer loss while
waiting for readiness. Preparation failure leaves pending input and its deadline
intact unless storage has settled it; it creates no failed Turn or input history.
During Start, consume preparation controls alongside the ordinary Run stream so a
control-only rejection or pending-start cancellation can settle promptly. Reuse
ordinary journal, receipt and completion/native-history persistence. Once cancellation
is sent, preparation errors/closure cannot replace its receipt or timeout path.
Pending-start cancellation uses the adapter's observed outcome after daemon handoff
and output forwarding. Missing or unconfirmed outcomes still fail conservatively;
preparation control errors cannot substitute for the cancellation receipt.
Do not fabricate an empty cancellation outcome or infer native quiescence.
The connection owner spans preparation and the transferred Run without a reservation-derived Run
deadline; every exit releases it.

The existing Worker discovers pending input with a connected, non-revoked device
in the same tenant when its Dispatcher has a connection resolver. An unbound
Session selects a device using the same engine capability checks as ordinary
work, including remote preparation support, then uses the existing immutable
binding before preparation. Existing bindings never move, including when their
device is offline, revoked or lacks a required capability. A missing device or
binding conflict leaves pending input and its deadline intact without a Turn;
other database/ownership errors stop the Worker. Preparation, claim, Run and cleanup occupy one of the same four slots as ordinary Turns, keyed
by Session. Both queues advance bounded ID cursors and alternate candidates; the
pending queue is scanned at most once every five seconds on the existing tick.
A preparation failure may retry while still pending, without extending its stored
deadline. This is private scheduling policy, not an upstream timing guarantee.
Unknown promotion results and errors after admission stop scheduling; existing
claimed-Turn reconciliation handles restart without another Start. Expiry retains
its ordinary cadence even at capacity. Public idle-text admission and principal
identity are implemented; initial inputs and complete public lifecycle remain separate.

The standalone service wires this resolver when its daemon gateway and
`AGENTS_API_EXECUTOR_URL` are configured. Construct the gateway and native registry,
configure the Dispatcher, then acquire Worker ownership before starting scheduling
or HTTP consumers. Registry construction does not call the ownership callback;
its Worker reference is assigned once before either consumer starts. Invalid
registry configuration therefore fails before acquiring the execution lease.
Shutdown waits for Run cleanup, drains registry observations while the Worker
still owns its lease, then releases execution ownership and the gateway.
The Codex resolver issues a fresh exact-Environment credential for the supplied
execution owner; it does not admit public Environment input or
define a transport for other engines.

#### Independent build artifacts

`make build-agents-api` produces `agents-api`, `agents-api-migrate`,
`agents-api-device` and `agents-api-environment-key` under `${PARSAR_HOME:-$HOME/.parsar}/build/agents-api`.
`AGENTS_API_BUILD_DIR` may select another absolute output directory. The build
uses only the explicit source set in `scripts/build-agents-api.sh`: the execution
service, its Go contracts and required shared daemon/logging packages, plus the
root Go module manifests. Product server/frontend, other applications and their
migrations/assets are absent from the temporary build context. Keep this boundary
explicit when introducing shared dependencies; do not copy the whole repository
to make an accidental product dependency compile.

The build uses Go directly with workspace discovery and CGO disabled, read-only
module manifests and trimmed paths. It requires no Node, Docker or product setup.
`make check-agents-api` runs this build before its tests, so the full `make check`
and the dedicated CI workflow enforce the same boundary. CI exercises the built
migration command and uses the built server for official-client HTTP checks.
`make docker-build-agents-api` reuses that build for Linux amd64 and sends only
its four executables and `services/agents-api/Dockerfile` to Docker. The pinned
Distroless runtime runs without root, a shell, product assets or an embedded
harness. Keep runtime credentials outside the image and migrations explicit.
`make check-agents-api-container` runs the existing official-client suite against
the image with a read-only root filesystem; it requires Linux Docker, a non-root
host user, the pinned SDK and a dedicated execution test database. Dedicated CI
runs this after binary validation. Changes to the image/build path require this
check in addition to `make check`; do not make ordinary Go builds require Docker.
Registry publication, additional runtime architectures, daemon packaging and
product cutover remain separate work.

`make build-agents-api-release` reuses the isolated build for a Linux amd64 archive
under `~/.parsar/`, with its four commands, license, operator guide, source/tree and
protocol manifest, and file/archive checksums. It requires clean committed source
and Python 3.9+, stages output privately, and packages fixed artifacts deterministically.
Keep runtime configuration, credentials, product sources and separately installed
daemons/harnesses out of the archive. Archive changes require content/hash and
fresh-extraction operator checks plus `make check`; execution acceptance uses the
packaged operators and public protocol, not private Store provisioning. Preserve
the database and native history when replacing the API package. This target does
not publish a release or provide an installer/supervisor.

`AGENTS_API_RELEASE_RUNTIME_IMAGE=sha256:<image ID>` adds an explicitly qualified
Linux amd64 Runtime to the same release builder. The Docker archive contains that
immutable image export, the committed seccomp policy and hosted operator guide,
with hashes in its manifest and checksums. The default Core-only archive remains
Docker-free. Image selection and packaging do not qualify an arbitrary image or
prove compatibility between unrelated Core/Runtime versions: accept the exact
package with a fresh database, extracted binaries, loaded image and real public
workflow. Keep model/operator credentials external and Provider ownership stable
across upgrades. This is the same managed Runtime, not user-managed enrollment.

#### Current implementation

The constraints below describe existing code, not requirements to preserve legacy
design. The [protocol assessment](contracts/agents-api/README.md#implementation-direction)
identifies replacements and gaps. Update these rules when their implementation is
replaced; do not carry obsolete compatibility code forward to satisfy this section.

- `internal/agentdaemon/gateway` is the shared daemon connection implementation.
  Its persistence interfaces use `internal/agentdaemon/device`, never product Store
  types. Product adapters live in `server/internal/agentdaemon`. Keep protocol
  frames in `internal/agentdaemon/proto` until the contracts directory migration.
  Store aliases preserve existing callers during this transition.
- Session deletion uses a durable `sessions.deleted_at` marker, committed with an
  existing Turn cancellation request under the tenant Session lock. Public reads,
  metadata changes, event streams and input admission exclude deleted Sessions;
  admission checks visibility under that lock before retry lookup. Creation keys
  remain reserved and cannot resurrect deleted Sessions. Missing/repeated deletion
  locally returns 404 and reuse of a deleted creation identity returns 409; exact
  hosted errors and overlapping stream timing remain unverified. Existing streams
  close when removal is observed without a fabricated deletion event.
  Internal Turn/receipt/finalization and restart reconciliation retain access so
  hidden work can settle under the existing execution lease. Queued deletion
  prevents claim; an already claimed execution may complete or receive cancellation.
  Confirmation does not guarantee native quiescence. Never revoke a shared device,
  remove a saved Agent or touch product data as part of Session deletion. Physical
  SQL/native history cleanup remains a separate required implementation gap; these
  records are retained, not claimed purged. Do not deploy a pre-deletion service
  against a database with deletion markers; migration rollback refuses to remove
  the column while deleted records exist, preventing public resurrection.
- `services/agents-api` owns its SQL schema, sqlc queries and embedded goose
  migrations. `AGENTS_API_DATABASE_URL` is required; never fall back to the product
  database URL. Its first persistence slice stores tenant-scoped Sessions with a
  stable engine and idempotent creation. It does not switch production execution.
- Reusable Agents have their own tenant-scoped `agents` records, independent of
  Session snapshots, engine bindings and product Agent definitions. The Store
  persists caller-validated non-secret configuration and metadata without applying
  harness capability restrictions or model defaults. Resource identity and equal
  initial creation/update timestamps come from the persistence boundary. The
  create primitive creates a fresh resource; public retry conformance remains
  unverified. Internal storage admission is 512 KiB for configuration and 64 KiB
  for metadata, not a claim about upstream limits.
- Vaults have their own tenant-scoped records in the execution database. Initial
  `POST /v1/vaults` and `GET /v1/vaults/{vault_id}` operations persist and read the
  resource without creating Sessions or contacting an engine. Omitted name is
  null; a supplied non-null string is trimmed and limited to 1–256 UTF-8 bytes.
  Omitted/null metadata becomes an empty object. Reuse the 64 KiB encoded metadata
  storage bound, without applying Session-specific pair/character limits. This is
  a local bound, not hosted parity. `GET /v1/vaults` lists the authenticated project's
  records using creation-time/ID keysets, default descending order and a default
  limit of 20 clamped to 1–100. Other resource limit policies are unchanged.
  Status accepts `active`/`archived` as a scalar or SDK `status[]` array, with both
  included by default. Private stored classification defaults existing/new rows
  to active; it is never exposed in the Vault response. Listing reads no Credentials
  and needs no encryption key or execution service connection. Mixed status encodings
  and repeated scalar parameters are rejected locally. Exact hosted errors, equal-time
  ordering and changes between pages remain unverified. Private archived fixtures
  prove filtering only: there is no public archive writer, archive timestamp or
  inferred delete-to-archive behavior. Retrieval, Session binding and dispatch retain
  their existing rules. Archive/revocation lifecycle remains a separate gap;
  do not introduce product roles or speculative lifecycle fields. Migration rollback
  refuses to discard classification while archived rows exist.
- Vault `DELETE /v1/vaults/{vault_id}` removes the project-owned parent and all
  Credentials through the existing foreign-key cascade in one SQL mutation. Do not
  loop through child deletions, decrypt secrets, require the storage key or call
  providers. Deletion applies to both stored classifications. Local missing/repeated
  deletion returns not-found; subsequent parent/child reads and new attachments
  cannot use the removed resources. Preserve Session snapshots, frozen choices,
  history and recorded retries. Later secret lookups fail without selecting another
  attached Vault or anonymous MCP. Already-resolved tokens and running Sessions
  are not revoked. Exact hosted archive, visibility, overlapping-mutation and error
  semantics remain unverified; row removal does not prove physical storage erasure.
- Static-bearer Credentials are children of tenant-owned Vaults in the execution
  database. Creation admits the owner in the same SQL statement as the insert;
  retrieval joins the owning Vault and selects public metadata only. No public
  operation decrypts or returns a token. Encrypt before passing secret values to
  SQL, using the execution service's separately configured random 32-byte key and
  the standard library's random-nonce AES-GCM. The versioned authenticated binding
  includes tenant, Vault, Credential, authentication purpose and exact destination.
  Never reuse product master-key conventions or daemon transport encryption for
  this storage boundary. Missing key configuration disables credential writes;
  malformed explicit configuration fails startup. See
  [`services/agents-api/credentials.md`](services/agents-api/credentials.md) for
  key persistence and current limits. OAuth and storage-key rotation remain
  separate gaps; resource creation never contacts the destination.
- `GET /v1/vaults/{vault_id}/credentials` lists safe metadata only, with both
  project and Vault ownership enforced on the parent, cursor and row query. An
  inaccessible parent returns not-found, even when the collection would be empty.
  Reuse the Vault status/limit parser and Credential metadata mapping. SQL must
  never select ciphertext for listing; no encryption key or execution is needed.
  Credential status is a separate private active/archived classification, defaulting
  historical/new records to active and never derived from the parent Vault's status.
  Synthetic archived fixtures prove filtering only. There is no public archive writer,
  timestamp or delete-to-archive inference; existing create/retrieve/token replacement,
  Session bindings and dispatch keep their rules. Migration rollback refuses to lose
  archived classification. Archive/revocation lifecycle, OAuth and hosted
  query/concurrency semantics remain gaps.
- Credential `POST /v1/vaults/{vault_id}/credentials/{credential_id}` replaces only
  the static-bearer token and update time. Require `auth.type=static_bearer` and a
  string `auth.token`, preserving opaque bytes; reject extra mutation fields before
  writing. Reuse safe metadata for the immutable encryption binding, then scope the
  atomic SQL mutation independently by tenant, Vault, Credential, static auth type
  and exact destination. Never decrypt the previous token or send plaintext to SQL.
  Missing encryption configuration or a failed write preserves the old row. Return
  the existing safe metadata projection; identity, name, destination, creation time
  and Session snapshots stay unchanged. Subsequent dispatch reads use the committed
  replacement through existing scoped lookup; already-resolved requests may retain
  the old token. This is not storage-key rotation, in-flight revocation or hot reload.
  OAuth and exact hosted concurrent-update/retry/timestamp semantics remain gaps.
- Credential `DELETE /v1/vaults/{vault_id}/credentials/{credential_id}` removes one
  owned row, including ciphertext, with tenant/Vault/ID checked in the same SQL
  mutation. It needs no encryption key, secret read or network call. Local reads,
  updates, listings and subsequent dispatch lookups cannot use that ID; missing
  and repeated deletion return not-found. Preserve frozen Session choices, retry
  identities and history without fallback to another credential or anonymous MCP.
  A token already read before deletion may remain in a dispatched request. Deletion
  does not revoke provider tokens, cancel Sessions or prove physical erasure from
  native history, WAL or backups. Do not infer a delete-to-archive mapping; exact
  hosted archive, post-delete visibility and repeat/error semantics remain unverified.
- Public reusable Agent create/retrieve uses `/v1/agents` and the same authenticated
  tenant/Beta-header boundary as Sessions. The resource envelope owns identity,
  timestamps and metadata, separately from saved configuration and Session state.
  Resolve known defaults and validate supported schema before writing. Preserve
  model/name/instructions verbatim, nullable fields and structured JSON numbers.
  Stored reasoning/service tiers, enabled multi-agent settings, JSON Schema output
  and deferred/tool-search/programmatic tools do not imply execution support.
  Reuse function wire validation, keeping Session execution restrictions separate.
  Model-derived reasoning effort is unresolved when omitted; do not infer it from
  the selected harness. Omitted/null service tier currently uses `auto`; complete
  upstream default/error/retry conformance and remaining MCP/web-search variants
  remain gaps. Unknown/unsupported variants fail explicitly. No product lookup is permitted.
- Public HTTP MCP uses the native harness client and tool loop. The supported
  execution profiles are Codex with `environment:none` or `self_hosted`, and
  Claude SDK with `environment:none`. Both require an explicit `service`
  connection origin and a trusted service-side harness. The execution device is
  part of the service deployment; an arbitrary caller executor cannot be relabeled
  service-origin. Admission requires the advertised `mcp_http_tools` capability
  during selection and again before claiming work. The `self_hosted` combination
  additionally requires `mcp_http_remote_environment` plus existing remote preparation
  capabilities, including at the daemon before the factory; individual MCP/remote
  capabilities on old peers do not imply the combination. MCP stays in the trusted
  service harness while workspace commands use the executor. Static Vault Bearer
  authentication additionally requires `mcp_http_remote_bearer_auth`; an older peer
  supporting anonymous remote MCP and environment:none authentication separately
  cannot execute the authenticated combination. Native remote readiness and exact
  MCP preflight both precede
  thread creation/resume. Other engines and placements remain implementation gaps.
  Claude SDK admission additionally applies the supported values described in
  [its adapter profile](#claude-sdk-adapter-foundation), including before persistence.
- Keep accepted public MCP credential profiles separate from private adapter
  capabilities. The execution service declares the verified public bearer profiles
  centrally; a daemon capability alone cannot open a public profile. Reuse frozen
  binding validation at admission and later input, then the same MCP capability and
  placement checks at device selection, final preclaim and request construction,
  before scoped decryption. Native configuration and token injection stay in the
  adapters. Add bounded shared checks while changing the related execution path;
  do not defer known duplication to a general engine or plugin framework.
- The shared MCP resolver preserves omitted/null `allowed_tools` as unrestricted
  and an explicit empty list as deny-all. Saved HTTP transport output includes
  `headers:{}`; the effective Session transport omits headers, matching the two
  pinned resource types. Saved-Agent updates never change existing Session
  snapshots; per-Session tools replace the whole field. The initial profile admits
  HTTP(S), boolean `required` (default false), empty/null metadata and empty/null headers.
  Static bearer authentication requires HTTPS and the attached-Vault rules below.
  Inline authorization, URL userinfo/query/fragment,
  other origins and stdio remain explicitly unsupported.
- Codex required MCP initialization additionally needs `mcp_http_required`, advertised
  only for the verified native pin and checked during selection, final preclaim
  and daemon dispatch/preparation. Preserve the boolean through typed messages,
  native rendering and exact configuration preflight. Reuse native required-server
  initialization during root thread creation and cold resume; send no native Turn
  until it succeeds, and never replace a failed strict resume with a new thread.
  Public work may already be accepted/queued/in progress while native initialization
  waits. This is not a continuing health monitor or a new public readiness state;
  exact hosted Session creation timing and initialization errors remain unverified.
- Send MCP declarations through typed daemon fields, independently of function
  callbacks. In Codex, a non-nil declaration replaces operator MCP options; use the existing
  native renderer and original tool names for `enabled_tools`, including `[]`.
  Before thread creation/resume, query native `config/read` with the exact cwd and
  reject additional servers or effective configuration differences. Disable native
  plugins/apps and select file-only MCP credentials; reject existing credentials
  in the private native home without deleting them or native history. Native
  reserved labels are an adapter restriction, not a saved-resource schema rule.
  Requests without this typed field keep the existing product behavior. The check
  is a snapshot on trusted service compute, not an atomic barrier against concurrent
  operator configuration changes. Discovery of a declared deny-all server can still
  contact it; deny-all governs tool exposure. Reuse neutral tool observations and
  the existing public `mcp_call` projection, never add a second MCP/model loop.
- The private Codex adapter's HTTPS MCP bearer authentication requires
  `mcp_http_bearer_auth` and the existing MCP/environment capabilities, checked
  before the factory. It is restricted to trusted service-side Codex with
  `environment:none` or the explicitly supported authenticated remote combination. A transient
  per-server `bearer_token` becomes a fresh daemon-owned `bearer_token_env_var`
  reference for each native process. Put the exact secret only in that app-server
  child's environment, after auxiliary launch probes; never in global environment,
  arguments, configuration/history, public snapshots or logs. Preflight accepts
  only the expected server/reference pairing and retains the existing rejection
  of ambient credential sources. Remote commands use the existing core-only native
  environment policy; service-side bearer variables must not enter executor
  environments, commands, files or native history/snapshots. Use the native HTTP
  client with TLS verification.
  This execution profile rejects empty values and bytes outside RFC 6750 b64token
  syntax with generic errors; it never trims tokens or narrows opaque Credential
  storage. OAuth and hosted redirect/error equivalence
  remain separate work.
- Session `vault_ids` omission/null/empty means `[]`; nonempty attachments must all
  belong to the authenticated tenant. Preserve caller order and public MCP
  `credential_id`. Saved Agents may store a nullable/nonempty credential reference
  without authorizing its use. Session admission resolves an explicit credential
  only inside attached Vaults for the exact declared URL, or selects the unique
  matching static credential when the ID is omitted/null. No match remains
  anonymous; ambiguity is a local 400 and unavailable references use the same 404.
  Resolve before any Session, initial input or event write. Freeze safe bindings,
  including anonymous decisions, in private Session configuration; never populate
  the public credential field from implicit resolution. At actual dispatch, recheck
  tenant, attached Vault, selected ID, static auth type and exact URL before scoped
  decryption. Metadata queries select no ciphertext; tokens enter only the existing
  transient daemon request. Selected authentication requires `mcp_http_bearer_auth`
  during device selection and the final preclaim check. Missing/wrong keys or
  binding failures never fall back to anonymous execution. Exact URL equality,
  immutable selection timing, implicit response population and hosted error/redirect
  semantics remain local decisions or unverified gaps. No new MCP loop is permitted.
- Public Agent updates use `POST /v1/agents/{agent_id}` with the same tenant/Beta
  boundary and shared saved-field validation. Preserve omission separately from
  null; only supplied fields replace saved values. Metadata is a separate whole-map
  replacement, with null/empty clearing it. Lock the tenant-owned Agent row while
  merging validated fields and enforcing the complete configuration bound, then
  commit configuration, metadata and update timestamp together. Never write a stale
  full snapshot over another update. No-field updates read without changing timestamps.
  Supplied nested fields currently replace the whole field and explicit null uses
  existing saved defaults; exact hosted nested/null and no-op timestamp semantics
  remain unverified. Model-derived reasoning defaults remain a separate gap.
  Neither updates nor retries modify existing Session snapshots or execution state.
- Public Agent deletion uses `DELETE /v1/agents/{agent_id}` and one tenant-scoped
  `DELETE RETURNING id` statement. Return the stored canonical ID with
  `object=agent.deleted` and `deleted=true`; missing/repeated deletion locally
  returns not found. It never deletes Sessions, history or runtime state and does
  not cancel accepted execution. Recorded creation identities still recover the
  accepted Session; new references cannot resolve an absent source. Historical
  identities retain their documented limitation. Exact hosted errors and ordering
  of overlapping source creation/deletion remain unverified; no tombstone or
  successful result is fabricated for an absent resource. Reject query/body data.
- Reusable Agent listing uses the same tenant/Beta-header and response mapping as
  create/retrieve. Page by `(created_at, id)` with a same-tenant saved-Agent cursor;
  listing never resolves Sessions, product objects or execution capabilities.
  Reuse shared list-query parsing. Agent requests accept positive int64 limits and
  return at most 100 records per page with accurate continuation; other resources
  retain their current 1..100 request rule. The local default is 20. Return the
  list envelope with data/has_more and first/last IDs (null for empty pages).
  Exact pinned upstream default/cap, empty-envelope and error semantics remain
  unverified; do not present local limits or generic SDK parsing as full conformance.
- Session `agent_id` lookup uses the authenticated tenant. Copy the saved resource
  ID and effective configuration into the immutable Session snapshot; saved metadata
  does not become Session metadata. Never look up the source Agent when reading or
  executing an existing Session. Omitted override fields inherit; supplied objects
  and arrays replace whole fields before defaults and execution admission apply.
  Reuse saved configuration validation and keep native capability restrictions at
  Session admission. Reject unsupported effective options instead of dropping them;
  an explicit supported replacement may make a saved configuration executable.
  Inline Sessions use the same admission path. New saved-reference Sessions and
  inline requests with Vault attachments or credential references record a separate
  caller-intent hash: source ID (empty for inline),
  supplied overrides (including field presence), environment/vaults, original metadata
  and normalized initial input. Exclude response streaming and resolved source values.
  Compare that same-tenant retry identity before looking up the source. A matching
  retry returns the existing Session without input admission or source revalidation;
  ordinary reads include current activity. Stream retries use the row's committed
  event cursor and emit no created event. Recheck after source resolution failure
  for a concurrently committed creator; do not hold a lock across resolution.
  The unique creation upsert remains authoritative when concurrent resolutions differ.
  Unrelated inline requests keep their existing resolved/default equivalences.
  Recorded credential-bound retries recover before reading mutable Vault contents,
  so another same-URL Credential cannot change an accepted selection. Rows with a
  known creator but without recorded request intent retain resolved-hash behavior;
  original overrides cannot be reconstructed, so no backfill is permitted. Source
  mutation-independent retries apply only to recorded identities. Exact hosted
  retry/error semantics remain separate work.
- Tenant scope must come from authenticated service identity before calling the
  execution Store. Product workspace/user references in metadata grant no access.
  Keep credentials and effective execution options out of Session metadata.
  Store resolved, non-secret Agent/environment configuration in the Session's
  immutable configuration snapshot. Inline and historical creation identities include
  the resolved configuration; new saved references use the separate caller intent.
  Session metadata updates replace only metadata under the authenticated tenant:
  omission is a read, null/empty clears, and a nonempty object replaces all pairs.
  Keep execution state, timestamps and the original creation request hash unchanged;
  creation retries return the current resource without restoring its old metadata.
  Public schema validation belongs to the API; the Store validates JSON structure.
- Session listing optionally filters by the immutable root `configuration.agent.id`
  within the authenticated tenant. IDs are opaque and include inline Agents; never
  require a surviving saved Agent or resolve product ownership. Apply filtering
  before pagination and activity projection, using the tenant/Agent/creation index.
  Omission retains unfiltered listing; a supplied empty string remains a filter.
  Preserve the existing tenant-owned cursor and creation-time/ID ordering rules.
  Hosted empty-filter, mismatched-filter cursor and exact error semantics remain
  unverified. Other resource lists do not accept this parameter.
- `make sqlc-generate` and the drift gate cover both services. Run
  `make check-agents-api` with `PARSAR_AGENTS_API_TEST_DATABASE_URL` pointing to a
  dedicated `parsar_agents_api_*_tests` database for Session integration tests.
  CI provides a separate PostgreSQL service. Migration immutability and ordering
  apply independently to each service directory.
- Turn writes serialize on the tenant-scoped Session row. An idle message starts
  a Turn; messages during queued/running/waiting work belong to that same Turn.
  Store input retry identities and order durably. Cancellation retains its first
  target, including an idle no-op, so retries cannot stop later work. Queued work
  can cancel before dispatch; active work needs an executor outcome. Terminal
  states and outcomes cannot be overwritten. Public event admission uses these primitives; live output streams remain separate.
- Input requests are ordered batches committed under the same Session lock. A
  retry key identifies the complete ordered batch; changed length/order/content
  conflicts and a failed transaction leaves no partial inputs or cancellation.
  Existing single-event requests retain their identities at batch position zero.
  Internal admission limits are 64 events and 512 KiB of payload per request;
  the public API must still validate the upstream event schema.
- The external protocol reference is `openai/openai-python`'s `beta/agents`, pinned
  in `contracts/agents-api/upstream.json`. Follow its Session/Turn/event semantics
  and verify supported behavior using the official client. Track current coverage
  in `contracts/agents-api/README.md`; SDK workflow objects are not this contract.
- Shared supported wire types live in `contracts/agents-api/v1`. `make openapi`
  separately generates the product spec and `contracts/agents-api/openapi.yaml`;
  never mix their routes or authentication schemes. CI checks both for drift.
- The standalone service uses `AGENTS_API_DATABASE_URL` and operator-provisioned
  SHA-256 API key bindings from `AGENTS_API_KEYS_FILE`. Each key resolves one
  organization/project and typed user/service-account principal. The internal
  tenant UUID is its project resource partition. Before starting the listener or
  Worker, atomically insert or verify the configured project-to-tenant bijection
  in `execution_project_scopes`; never remap or delete existing associations when
  keys change. Configuration requires explicit identities, with no legacy default.
  Optional `OpenAI-Organization` and `OpenAI-Project` headers must match the key;
  repeated/conflicting values fail authentication. Metadata, forwarded identities
  and product session cookies grant no access. Keys can rotate under the same
  principal; changing or removing caller bindings requires a service restart.
  Every new Session requires an explicit typed creator at the Store boundary,
  including internal callers. Public creation derives it only from the authenticated
  principal. Persist creator kind/ID in the creation transaction and never rewrite
  them on retry, update or source mutation. The tenant remains the project partition;
  do not duplicate project identifiers or create a product identity dependency.
  Both early saved-reference recovery and the authoritative creation upsert require
  matching creator kind/ID before returning a Session or event cursor. Different
  credentials for the same principal can retry; another principal using the same
  project/key receives the local idempotency conflict. This does not introduce
  creator-only resource reads or mutations, or claim verified hosted retry parity.
  Pre-migration Sessions retain null creator columns and remain project-readable;
  creation retries cannot claim them. Missing creator and missing request intent
  are distinct. Never infer historical ownership from keys, metadata or product
  records. Retire older API writers before serving the creator-enforced deployment;
  mixed-version writers are not supported. Tests must supply explicit synthetic
  creators; only controlled historical fixtures may seed unknown ownership.
  The operator-selected
  `AGENTS_API_ENGINE` is separate from the requested model.
  Public execution supports Codex and Claude SDK with environment `none`, plus
  the Codex self-hosted text/function profile and the qualified Codex/Claude dedicated
  Docker hosted profiles; reject unsupported
  input/environment/agent options explicitly.
- `packages/agents-client/v1` configures the pinned official `openai-go` Session
  service. Use SDK request/response types, pagination and errors directly rather
  than reimplementing transport or copying wire types. Supply an explicit service
  base URL/key and creation retry key; SDK retries are disabled by default. Product
  integration is a later cutover, not a side effect of constructing this client.
- `services/agents-api/tests/official_client.py` verifies the actual server with
  the pinned SDK and strict response validation. It requires a dedicated test DB
  prepared by the Store tests and `AGENTS_API_SERVER_BIN`; it never starts Docker.
  The same harness runs the official Go client with fresh execution tenants and
  checks its created Sessions through the Python SDK.
- Execution devices are operator-provisioned in the Agents API database with
  tenant ownership and a credential digest. Their internal daemon gateway uses
  `/api/v1/agent-daemon/*`, separately from the official `/v1/agents/*` surface;
  device credentials grant no Session API or product permissions. The optional
  `AGENTS_API_DAEMON_WS_URL` enables that gateway. It is a single-process registry,
  not a claim of multi-pod execution or the public self-hosted executor protocol.
  Session/device bindings are tenant-scoped and immutable. Revocation denies new
  connections and binding reads; an existing connection closes on its next
  heartbeat. Connectivity comes from the live registry, not a persisted online
  flag. `last_seen_at` is diagnostic only. Product gateway behavior is unchanged.
- `services/agents-api/internal/execution` dispatches internally resolved Turns
  through that gateway. Claim `queued` to `in_progress` before subscribing/sending;
  never automatically replay a claimed or interrupted Turn. Ordered extra inputs
  require native steering receipts. Commit terminal outcome and native Session ID
  together under the admission lock; unapplied messages prevent successful completion.
  Resolve credentials separately from the immutable non-secret snapshot.
- Internal execution requires the advertised `durable_turns` engine capability,
  strict resume, and optional cancellation receipts
  and `release_on_completion` support. Reject unadvertised peers before claiming;
  failed strict resumes must not fall back to a new native thread. Release the native writer before forwarding
  completion, so the next Turn can resume its durable native ID. For
  release_on_completion Runs, close new steering admission and finish all
  existing steering receipt sends before releasing the executor and forwarding
  Done. Cached input identities and conflicts remain readable while completing;
  queue/write success is not consumption. The receipt worker stays busy through
  its send, and router shutdown cancels its native and transport waits.
  `durable_input_receipts` is required before execution binding/claiming. The
  per-input `durable_receipt` opt-in requires release-on-completion and a phased
  adapter. Its ten-second transport timer stops only after a complete native write;
  a separate `written` acknowledgement stops the API's thirty-second delivery timer.
  Neither that phase nor legacy `in_flight` advances the input cursor. Await final
  native acceptance/consumption under the Run lifetime without automatic redelivery.
  Receipt sends retain a separate five-second shutdown-aware context, and Done
  retains a fifteen-second final settlement bound. Once cancellation is sent, its
  receipt owns the terminal outcome even if an input becomes unknown first.
  Calls without the opt-in retain their existing response deadlines. Existing product
  requests retain their default idle-process policy. Native history still requires
  the device's persisted engine files; IDs alone cannot restore deleted history.
  Cancellation receipts carry the stopped engine's continuity snapshot when no
  Done is emitted. Preserve separately reported usage on failure; do not add the
  same counters again when Done also includes them.
- The dispatcher is an internal entry point used by the standalone service worker.
  Its private `daemon` configuration is neither `environment:none` nor the official
  self-hosted executor protocol. Further pending interactions and provider allocation
  remain separate slices. Unexpected interaction requests fail explicitly until supported.
- `environment_none` advertises an adapter's explicit environment-disable
  path. Execution snapshots with public `environment.type=none` require that
  capability and set `disable_execution_environment` on the internal prompt.
  For Codex, the daemon forces `CODEX_EXEC_SERVER_URL=none` after caller environment options
  and confirms native `local` and `remote` environments are unknown before starting
  or resuming a thread. Unsupported binaries fail closed. The bound device hosts
  the engine process; it is not a user execution environment. This is not an OS
  isolation guarantee, and engine state still lives on that host. Ordinary product
  requests retain their existing environment. The public worker selects an authenticated same-tenant engine host for this mode.
- `execution_controls` advertises the typed search/verbosity block on the daemon
  prompt. Agents API requires it in addition to the selected engine's required capabilities
  before binding/claiming work. Older peers with only option-based capabilities
  must not receive controls they would ignore. The API sends resolved search and
  text verbosity values; native option names belong to adapters. Codex translates
  them using its existing validation/catalog path after cloning operator options,
  so explicit controls take precedence without mutating those options. Omitting
  the entire block preserves ordinary product behavior; a supplied block requires
  both valid fields. This internal contract does not add public configuration or
  engine support. Future native adapters must verify the same semantics before
  advertising the capability.
- `web_search_control` advertises the Codex adapter's explicit `web_search` option
  (`disabled`, `cached`, or `live`). Agents API requires this capability before Codex dispatch;
  the typed execution controls force search off on new and resumed Turns. Native configuration translation stays in the
  adapter. Product requests that omit the option inherit their existing defaults.
  This is tool selection, not a network isolation guarantee.
- Inline Agent `text.verbosity` accepts `low`, `medium` and `high`; omitted or
  null values resolve to `medium` in the immutable configuration snapshot. The
  Codex dispatcher requires `text_verbosity` support and sends the effective value in
  typed execution controls through the Codex adapter for both new and resumed Turns. The adapter queries
  the native active catalog with `codex debug models`, checks model support and
  pins that catalog snapshot for execution. The probe requires Unix process-group
  cancellation; other daemon hosts do not advertise this capability. For models
  without declared verbosity support, including the native unknown-model fallback,
  `medium` selects native default text generation by omitting the override. The
  pinned protocol defines `medium` as the default text amount. Supported models
  still receive explicit `medium`, even when their catalog default differs.
  Unsupported `low`/`high` and unreadable catalogs fail before model execution;
  unsupported non-default levels remain an explicit implementation gap.
  Product requests that omit the native option retain their existing defaults.
  Structured output formats remain a separate protocol gap.
- `subagent_control` advertises native subagent tool control. Agents API requires
  it when resolved `multi_agent.enabled` is false and sends the typed internal
  `disable_subagents` policy on both new and resumed Turns. Native translation
  stays in the adapter: Codex disables both multi-agent feature generations,
  overriding operator feature preferences. Product prompts that omit the policy
  retain their defaults. Enabled multi-agent execution and public Subagent
  resources remain separate implementation gaps; the Agent tools list is not
  proven to enumerate every harness-internal utility.
- Private `observe_subagent_identities` requests discover direct root children
  from completed native spawn/resume Items. The Codex adapter verifies exact child
  identity, persisted parent and original spawn-source parent against its RPC-bound
  root. Use parent-filtered persisted `thread/list`; `thread/read` can synthesize
  creation time before persistence. Native fields stay in the adapter. One worker
  allows 64 candidates and 64 metadata RPCs per dispatch, four 100-row pages per
  lookup pass, and three seconds per lookup. Root terminal content and Usage freeze
  before a separate, three-second settlement wait; keep the reader free for RPC
  replies and deliver successful observations before Done. Owner cancellation,
  missing persistence, overflow, failed spawn and late discovery remain explicit
  gaps, never invented identities or public closure. Child lifetime is unchanged.
  The leased execution journal projects neutral identity facts in its existing
  Session transaction; unrequested observations are rejected. Device and engine
  come from the authorized Session binding, not daemon-supplied project ownership.
  The service assigns a stable ID unique within device/engine/native identity and
  freezes Session, parent, native creation and first-event provenance. Conflicts
  roll back the whole event batch; identical or later continuation observations
  preserve the original binding. Internal reads enforce project and visible Session
  scope. This is a private consumer prerequisite, not public Subagent admission,
  lifecycle, child output reconstruction or complete discovery/recovery.
- `function_tools` advertises the optional native function-call bridge. Explicit
  prompt definitions become Codex dynamic tools; unchanged prompts carry none.
  Requests and ordered text/image results are scoped by Run and native call ID.
  Normalize string results into one text part at the public execution boundary;
  the internal result carries a typed content array, and adapters translate it
  to native content without fetching images or dropping parts. Validate content
  before consuming a pending call. Retry identity includes the complete ordered
  content and success flag. Reuse
  application receipts and conflict detection; a receipt confirms the native
  reply was written, not that an external side effect succeeded. Pending calls
  end with their Run; the execution service owns persistence and recovery, while
  Parsar retains business approval and credential-owner authorization. Do not
  map native approval requests to invented official protocol resources.
- Function-call storage is scoped by authenticated tenant, Session and Turn, with
  immutable public/executor call identities and arguments. Result admission and
  application receipts serialize on the same Session lock as cancellation and
  terminal transitions. Store the complete caller-validated result object; wire
  validation and native translation belong to their API and execution boundaries.
  Identical retries return the saved decision; changed results conflict. Pending
  reads exclude applied calls and cancelling/terminal Turns while history remains
  readable. Persistence does not imply transparent native-process recovery.
  Recording a call moves the Turn to `waiting`; the last application receipt
  resumes it. Session reads use one database snapshot for Turn, actions and usage.
  Session state events retain their action snapshot, without private executor IDs
  or results. Cancelling/terminal Turns expose no actionable calls. A waiting Turn
  can fail or cancel before a result arrives; successful execution requires resume.
  Actions remain visible until native application is acknowledged. This timing is
  an implementation choice, not verified upstream event sequencing.
- Function results can join internal message/cancel input batches. Their explicit
  Turn/call identity selects an existing call; admission never creates a Turn for
  a result. Save the complete result and its input retry record in the same Session
  transaction. Any invalid target, conflicting result or later batch error rolls
  back the whole request. Identical saved results remain retryable after termination
  without applying them again. The execution input cursor skips function results;
  their separate native receipts still determine application. Public result events
  validate variant-specific fields and required values before admission; retain
  omitted versus null error/output and ordered text/image parts. Inline function
  tools resolve into the immutable configuration with explicit
  `defer_loading=false`. Validate required strings and parameter objects before
  persistence; reject unsupported deferred discovery. Omitted/null/empty tool
  lists resolve to no tools. The public worker selects or waits for a same-tenant
  device advertising `function_tools` when the Session has functions.
  Function results are Session input Items: emit `item.added` without an output
  index, and never emit `item.done`, whose upstream union only allows agent output.
  Project their public output/error from the saved submission, including missing
  versus null fields; native content normalization must not change public history.
- Internal function execution requires an advertised `function_tools` capability
  before claiming a Turn. Translate resolved definitions in the execution adapter,
  persist declared callbacks before exposing actions, and deliver each saved result
  once per live dispatch. Keep its success flag and ordered text/image output;
  append a non-null error as a final text part because the native result has no
  separate error field. Retain the original complete result in storage. Do not
  treat transport delivery as application or automatically replay an uncertain
  result. The adapter waits for outstanding application receipts even when Done
  arrives first. Waiting Turns still accept execution observations and cancellation.
  The public function workflow is verified with the pinned SDK and a real daemon
  and Codex process against a synthetic model endpoint. This does not verify
  other tool types, deferred discovery or upstream service timing.
- `message_items` advertises native assistant-message observations. Agents API
  opts in with `observe_messages` only for advertised peers; ordinary product
  requests retain their existing frame sequence. Opted-in text deltas carry their
  native item ID, and `output_message` records start/completion, phase and the
  completion text snapshot. A snapshot is not another delta; uncompleted messages
  remain partial when their Turn ends. Keep these observations in the journal
  before projecting public Items. This does not promise daemon event replay.
- Legacy `tool_items` / `observe_tools` raw snapshots remain available to old
  daemon callers. New Agents API execution does not request or decode them.
- `tool_observations` advertises engine-neutral tool snapshots. The opt-in
  `observe_tool_observations` takes precedence over legacy `observe_tools`:
  attach the typed `observation` to existing tool-call frames without `native_item`.
  Native adapters own discriminator/status/action translation and preserve raw
  structured values; reuse the shared function-result content type. Kinds are
  `command`, `mcp`, `function` and `web_search`; observation status is
  `in_progress`, `completed`, `failed` or `incomplete`. A present empty function
  content array remains distinct from missing content. These are
  execution facts, not public Items: the API owns public IDs, schema projection,
  lifecycle events and persistence. Product requests that omit the opt-in keep
  their frame sequence and fields. Agents API requires this capability before
  claiming work and always requests neutral observations. Its Item projector
  validates this shared contract and never decodes engine-native tool snapshots.
- Codex callers opting into neutral tool observations also receive `command_output`
  fragments with the existing native command identity. The adapter filters the root
  Thread/Turn; the service requires an already indexed command in the same Turn.
  Journal and Item updates commit with `agent.output.command_execution_output.delta`
  events, retaining original fragments and the command's stable output index.
  Completion output replaces accumulated drafts; absent completion output retains
  observed text. Terminal Items ignore late fragments, and cancellation preserves
  partial output without inventing successful command completion. Native text
  conversion and output quotas still apply; this is not a byte-complete stdout/stderr
  guarantee. Pinned native 0.153.4 also has an early-output subscription window;
  missing native notifications/aggregate bytes remain a separate execution gap,
  not output to reconstruct from model tool-result prose. Older peers may supply
  only completion snapshots. Product requests
  without the observation opt-in retain their existing frames.
- Execution observations are written to tenant-scoped `turn_events` in ordered,
  idempotent batches before they can back recovery or publication. Keep daemon
  payloads intact; this internal journal is not the public SSE protocol. Flush at
  least every 100 ms while consuming events and before terminal persistence;
  uncommitted observations can be lost on a hard process crash. Terminal outcome,
  journal entry and native continuity commit together. Preserve partial text on
  cancellation, including frames queued before a separate cancellation receipt.
  Do not infer successful completion after a persistence error or stream overflow.
- Agents API uses the gateway's durable subscription; overflow or disconnection
  closes it with an explicit error. Product subscriptions retain their existing
  best-effort behavior. Journal limits are 512 KiB per payload, 1 MiB per batch,
  65,536 observations and 32 MiB per Turn; terminal persistence reserves one
  additional outcome entry. These are internal admission limits, not promises
  about upstream API limits or durable daemon-to-service replay.

- Live Session SSE reads execution-owned `session_events`, committed with the
  corresponding input, Item or lifecycle transition under the Session lock.
  Store immutable transition snapshots; never render an old event from a later
  Turn state. Reuse the API's response mapping and keep internal snapshots out of
  wire payloads. Historical index rebuilding emits no live events.
- The notification buffer retains at most 256 events and 64 MiB per Session
  after each transaction, retaining a single oversized event if necessary.
  Read batches are bounded to 32 events / 1 MiB, with the same single-event
  exception. This buffer is not a public replay log: GET begins at the committed
  high-water mark, ignores Last-Event-ID, and polls committed events every 100 ms.
  Missing sequence positions produce a safe stream error and close; recover via
  Session/Turn/Items queries. Socket writes have a five-second deadline and hold
  no database connection. Client disconnect releases the handler; comments keep
  idle connections alive. SSE does not close merely because one Turn finishes.

- Session creation with `stream=true` reuses atomic input admission and the live
  event loop. The upsert returns its cursor under the Session lock, before initial
  inputs; never replace it with a post-commit cursor lookup. A new response emits
  one request-local `agent.session.created` with the pre-input resource snapshot,
  then committed changes from that cursor. The local creation retry key excludes
  response mode: retries observe only later events and admit no work again. Retry
  the same request/key with `stream=false` to recover a lost Session ID. GET event
  streams retain their current live-only start. Disconnect never cancels admitted
  work. Exact upstream created-snapshot timing, POST stream lifetime and creation
  retry response semantics remain unverified; the separate SDK one-Turn helper
  does not define this endpoint. Do not present local retry behavior as replay.

- Public Turn retrieve/list project persisted execution state and the immutable
  Session Agent identity. Scope both resources and pagination cursors to the
  authenticated tenant and Session, ordering by creation time then ID. Do not
  expose adapter outcomes, native IDs or raw errors. Failure uses a customer-safe
  category; usage is nullable when a complete upstream breakdown is unavailable. Submission uses the separate Session events endpoint.


- Public Items list reads a persisted execution-owned projection, updated in the
  same Session transaction as admitted messages and journal batches. IDs derive
  from the Turn and source identity; the first-observation timestamp and stable
  tie breakers never change when content or status changes. Allocate each new
  Item's Session position under the Session lock, preserving observation order
  for equal timestamps. Allocate a separate zero-based `output_index` per Turn;
  inputs do not consume output indexes. Updates and retries retain both values.
  Existing indexed history keeps its pre-upgrade deterministic order; missing
  original ordering cannot be reconstructed. Cursors are scoped to the authenticated Session.
  Terminal Turns expose unfinished Items as `incomplete`, preserving completed
  message/tool states independently of the Turn outcome.
- Public history reads use the persisted index; the private pre-Items journal
  backfill is retired. Migration 15 rejects unprepared historical Turns before
  removing the obsolete indexing marker. Prepare them with release `906069e`
  before upgrading, following `services/agents-api/README.md`. The migration
  preserves indexed Item payloads, positions, output indexes and source journals;
  never mark unprepared history indexed by hand or replay engine execution.
- Project only the declared public Item variants; native adapter metadata is not
  a response schema. Preserve structured tool JSON without float conversion.
  Completion text replaces accumulated deltas. Item merging must not mutate the
  incoming observation or the previous snapshot: public text delta events read the
  original fragment after merging, while Items retain the accumulated text.
  Copy the content slice before replacing its text pointer. Keep partial output on termination;
  do not turn an unfinished call into a successful result. Thinking fragments are
  internal observations, not a claim of upstream reasoning-item support.

- Public `POST /v1/agents/sessions/{session_id}/events` accepts ordered text-message
  and cancellation batches through the pinned official client. Preserve individual
  input messages in the Item index while deriving text for native dispatch. Batch
  idempotency and cancellation targets remain durable; unsupported variants fail
  before admission. Session creation accepts initial text as a string
  or user-message array through the same parser and admission path. Commit the
  Session, initial input, first Turn and Item/event projections in one transaction.
  A creation retry returns the existing Session without re-admitting initial work,
  including after terminal or later Turns. Omitted/null input retains idle creation.
  Creation streaming uses the shared live path above; non-text messages remain a gap.
- Enabling `AGENTS_API_DAEMON_WS_URL` also starts a bounded execution worker. Select
  only connected, capable devices owned by the authenticated tenant; bind once and
  preserve native continuity. Metadata cannot select a device. Offline work stays
  queued and can be cancelled. An engine host is not a self-hosted environment.
- One worker service owns an execution database through a dedicated PostgreSQL
  advisory-lock connection. Its execution Store view uses that same connection for
  every Session transaction: binding, claim/reconciliation, journal/Items/Usage,
  function callbacks/application receipts and terminal/native continuity. Serialize
  these short transactions and lease pings; execution transactions have a five-second
  deadline including gate and Session-lock waits. Never hold a transaction across
  daemon/model work, reconnect the writer or fall back to the pool after lease loss.
  The original Store handles public admission and device/auth maintenance on pooled
  connections. Execution reads may also use the pool; a read grants no write authority.
  At startup, reconcile previously claimed work as failed, preserve queued inputs
  and never replay uncertain execution. Shutdown cancels active dispatch and attempts
  terminal persistence before releasing the lease; a lost owner cannot commit it.
  Lease Close invalidates its writer and waits for pgx connection cleanup within
  the caller deadline. A later Close can resume that wait after a timeout. This
  drains client resources; it does not acknowledge remote advisory-lock release.
  Worker shutdown retains its existing bounded best-effort close policy.
  This fences database writes, not already queued daemon commands or native effects.
  Native quiescence/reconnect and recovery of unreported outcomes remain separate
  gaps; this is not distributed exactly-once side-effect execution.
- Session state and last activity derive from its latest persisted Turn. Queued or
  active work is `in_progress`, successful/cancelled work is `idle`, and failures
  use a safe public error. The worker does not replace product dispatch, business
  authorization, or the separate approval/environment lifecycle work.

### SDK subprocess ownership

The shared daemon `clirunner` offers opt-in Unix process-group ownership for
adapters whose SDK launches a native child. Existing callers keep their current
process policy. Explicit cancellation and parent-context cancellation share a
TERM grace period and bounded KILL escalation. An internal reaper also cleans
remaining group members when the direct process exits, even if a descendant
still holds stdout open. During cancellation, surviving descendants keep the
remaining TERM grace after the leader exits. Unsupported hosts reject this mode before launch.

Owned output pipes remain readable after the leader exits. Consumers must drain
stdout and stderr before calling `Wait`, which joins the cached process result
and closes the readers. `Done` reports leader reaping and group cleanup signals;
it is not a native execution receipt or proof of persisted history. SDK adapters
must close their query, await their native child and drain observations before
publishing completion. Process groups are lifecycle supervision, not OS isolation
or containment of descendants that deliberately leave the group.

### Harness qualification and onboarding

Codex, Claude and future harnesses have equal architectural status. The common
Runtime wire protocol and Factory/Session/Prepared interfaces own lifecycle,
input receipts, cancellation, recovery and resource access; each native adapter
retains its implementation and model/tool loop. A new engine supplies an adapter,
a qualified profile in `services/agents-api/internal/engine`, registration and
an independently verified deployment. It does not add engine-name branches to
API handlers, persistence, dispatch or scheduling.

Agents Core is pre-release. Replace superseded internal interfaces and execution
paths cleanly; do not retain version fallbacks or compatibility shims. Preserve
the pinned official public protocol, valid data and still-used infrastructure.

The small static profile catalog owns engine-specific public admission and value
limits. Profile callbacks are pure and use existing public/protocol types; they
cannot query business data, decrypt credentials or control native processes.
Public schema validation, qualified engine support and actual Runtime capabilities
remain separate. Runtime advertisements alone never enable public operations.
Shared dispatch checks capability combinations, not a whitelist of engine names.
Harness onboarding does not require feature equality. Verify common lifecycle
obligations and use the same public assertions for each declared operation,
retaining native isolation tests where appropriate. Optional native differences
remain independently prioritized capability/protocol work, not onboarding blockers.
Keep the complete pinned public protocol target and accepted functionality intact.
Never equate accepted parameters with applied native behavior.

The base daemon `Session` owns cancellation. Permission and user-choice response
methods are optional `PermissionResponder` and `UserChoiceResponder` interfaces;
an adapter only implements them when it emits those interactions. Unsupported
responses receive a negative receipt, never fabricated application. The router
retains its existing interaction routing and retry ownership.

`execution.Policy` supplies immutable service qualification to HTTP admission,
Worker device selection and final dispatch. Custom service composition must give
the same Policy to `api.WithExecutionPolicy` and `Dispatcher.Policy`. Zero values
use built-in profiles; an explicitly empty catalog authorizes none. Runtime
advertisements cannot add service profiles. No mutable global registration or
compatibility fallback is permitted. The synthetic third-harness acceptance under
`services/agents-api/internal/store` exercises the actual gateway and daemon router;
its fixture under `apps/parsar-daemon/testdata` is never a production engine.
See [the integration guide](contracts/agents-api/harnesses.md) for contract and
operation-specific acceptance and the [onboarding reference](contracts/agents-api/harness-onboarding.md)
for implementation and registration steps.

MiniMax Code's opt-in Agents API profile qualifies native ACP 0.4.12 for
`environment:none` text execution. It reuses the shared lifecycle without public
workspace, functions, MCP or native Subagents. Native configuration disables
file/shell authority and external
capability discovery; the child receives a private Session home and a restricted
environment. Active-input application requires a native ACP receipt, cancellation
settles the process and output, and continuation requires the exact owned native
history. Do not infer history IDs or qualify hosted execution from this text
profile. See [deployment and acceptance](services/agents-api/deploy/mcode/README.md).

The MiniMax workspace profile retains the published CLI and isolates native
workspace tools behind its standard MCP client. The process and native Session
share one private control directory; public workspace files cannot configure that
process or become privileged project instructions. A trusted adapter-owned bridge
runs the original six tool implementations in the upstream Linux sandbox, with no
unsandboxed fallback. Keep native history bound to the control directory and Files/
Artifacts bound to the public workspace. Core and shared file helpers remain engine
neutral. This internal MCP transport does not admit public MCP configuration.
Record published CLI and worker-source provenance separately; complete
[workspace acceptance](contracts/agents-api/mcode-workspace-v1.md) before enabling
hosted execution. The standalone companion uses its own npm lock; `make check`
runs its lifecycle tests and script checks, while its exact-source Linux build and
Docker qualification (including `native.test.mjs` under both network policies)
are required when the companion changes.

Claude hosted functions compose the existing SDK function bridge with the native
workspace sandbox. Only declared function tools and the verified native tool
inventory are available. The bundle advertises this combination separately from
basic workspace execution; function preparations require that verified combination.
External hosted MCP remains unqualified. Function callbacks do not change file,
credential, history, subagent or network authority.

### Claude dedicated Docker Runtime

Build the pinned SDK bundle with `scripts/build-claude-sdk-runtime.sh`, then use
`scripts/build-claude-runtime.sh` with the existing shared workspace helper build.
See [deployment and engine onboarding](services/agents-api/deploy/claude/README.md).
The helper executables retain their historical Codex names; their local directory,
write and export operations are shared and do not launch an engine.

`PARSAR_CLAUDE_SDK_WORKSPACE=managed` requires the shared dedicated local binding,
canonical workspace and its same-inode `/workspace` mount, and explicit immutable
network policy. Native history, home and scratch live separately under
`PARSAR_HOME/runtime/claude-sdk`; daemon authentication stays under
`PARSAR_HOME/parsar-daemon`. The trusted image and protected staging directory
remain outside writable workspace roots. No product state or native user profile
is imported. The separate unbound `environment:none` profile keeps its behavior.

The Docker operator option `nested_sandbox` is false by default. The qualified
Claude image requires it: Docker supplies an init process and permits nested procfs
mounting by removing its outer `/proc` masks/read-only submounts. `/sys/firmware`
and powercap remain masked; the root and sysfs mounts remain read-only, capabilities
remain dropped, and the existing seccomp/no-new-privileges policy remains enabled.
Do not enable privileged mode, weaken the native sandbox, mount host process state,
or apply host-global policy changes. This is an image deployment prerequisite,
not a public API option or an engine-name branch in the Provider.

The SDK adapter advertises `local_runtime_v1` only for its Linux bridge contract.
Registration combines that contract with the verified operator binding. Core uses
an explicit accepted engine profile independently of advertisements. This profile
supports native Bash/Read/Edit, preparation, shared Files/Artifacts, cancellation
and same-history continuation. The separately advertised `workspace_functions`
combination supports declared public functions with text results. External HTTP
MCP remains unqualified here; its existing `none` support is retained.
Native Bash network access uses the harness's HTTP proxy; no alternate networking
or tool loop is implemented by Core.

Recovery uses the SDK's history APIs. An explicitly supplied native identity must
exist. If Core requires existing history without having recorded an identity, the
adapter accepts only one nonempty native history for the exact bound cwd. Missing,
foreign, ambiguous or metadata-only history rejects before model input. The Runtime
volume and shared Environment/Session binding establish ownership; this lookup
cannot select another Session's home or infer ownership from a model response.

### Claude SDK adapter foundation

`packages/claude-sdk-adapter` privately owns the pinned official TypeScript SDK
and native message translation. The Go `claudesdk.NewFactory` uses the shared
owned process runner and emits the existing daemon delta/error/Done frames.
The SDK owns the model loop. Its narrow stdio protocol carries a start request,
text deltas, function calls/results/receipts, active text input/receipts, usage
snapshots and one terminal result/error; native translation stays inside the adapter.
With `observe_messages`, it also emits the existing neutral `output_message`
start/completion snapshots and tags deltas with the native Messages API message
ID, not the SDK event UUID. Text blocks in one native message share that identity.
The SDK's per-block assistant snapshots replace draft block text; only native
`message_stop` completes the message, without replaying its text as another delta.
Thinking/tool-only messages produce no text Items; interrupted messages retain
their streamed partial text. No phase is inferred from the final result.
SDK/native child release and output draining precede daemon completion.

`claudesdk.Config.Workspace` is a private, trusted operator binding for one
qualified placement. It enables native Bash/Read/Edit and declared host functions
in the existing SDK loop. The entire factory must already run inside an outer mount/process boundary
that excludes application, daemon and other-tenant credentials and host policy.
The factory does not create that boundary. The dedicated Docker profile below
selects this binding at startup; cwd and request options cannot select its policy.
The workspace, managed history, runtime home, scratch and protected secret roots
must be pre-existing canonical, separate directories. Runtime code and dependency
search paths must remain outside those roots and be read-only in the placement.
The complete packaged runtime directory, including `node_modules`, must not
overlap any bound root. Its bridge uses the packaged `dist/main.js` layout.
Node, the bridge entrypoint and its readiness companion must use canonical file
paths. Dependency aliases outside mutable roots are resolved before use in PATH;
aliases within mutable roots are rejected even if their current target is safe.
The operator owns allocation, exclusive use and retention; a binding is not
per-Session authorization, a tenant boundary or an idle Files owner.

For this profile, `Config.Env` replaces inheritance for both readiness and
execution, selecting only supported provider/proxy variables. The adapter fixes
HOME/history/scratch, native enforcement flags and tool inventory; the bridge
request contains variable names, never credential values. Native Bash uses the
strict sandbox with no fallback or weaker isolation. Separate native file-tool
permissions and a session tool hook restrict Read/Edit to the bound workspace
and deny protected roots; background/unsandboxed Bash requests are rejected.
The deployment must retain these controls, including the SDK-owned hook.
External MCP and remote-environment combinations remain rejected in this private
profile until separately qualified. The existing `none` profile retains its
behavior. Packaged `workspace_tools` establishes bridge support only, not host
isolation or a public capability. The dedicated Runtime integration composes
public preparation, shared placement quotas, command Items and Files ownership.
`TestLiveClaudeWorkspaceFactory` is explicit real-provider acceptance inside a
qualified placement, including effects, cancellation and same-history continuation.

The private workspace bridge also accepts `prepare` without a prompt. It freezes
validated configuration and resume identity, checks required history, and retains
one native process through the pinned SDK's `startup`/`query` API. Preparation
requires initialization and acknowledgement of the required hooks while the input
iterator remains empty. Its `prepared` receipt permits one later `start` containing
only the initial prompt; configuration replacement, premature or duplicate start
is rejected. Native Session identity and actual tool inventory are still checked
at execution initialization before `input_ready`. Existing direct execution uses
the same observation and completion path.

Unused EOF, owner signals, invalid control input and native exit release owned
resources before a terminal event. The private `workspace_prepare` runtime feature
identifies this bridge contract only. It does not register daemon preparation,
enable public admission, or project public Environment readiness. Preparation may
write native runtime metadata outside the workspace and perform startup traffic;
it does not prove provider authentication, complete sandbox health or tenant
placement authorization.

`claudesdk.NewPreparationFactory` privately binds that workspace bridge to
`agent.Prepared`; the existing workspace factory uses the same Prepare/Start path.
Preparation receives configuration and required resume identity without a RunID or
prompt, checks the installed `workspace_prepare` feature, and owns the process until
one successful Start transfers it. The selected configuration and environment are
fixed before returning; a later Start supplies only its actual RunID, prompt and
output channel. Early preparation failure returns without an executing Session or
fabricated completion. The non-workspace direct factory keeps its existing path.

The preparation owner context spans the eventual Session. Start's context bounds
that operation only, and Close is inert after successful transfer. Abandoned or
failed preparation, owner cancellation and native exit release owned work. The
same output consumer and cancellation settlement follow the resource across Start;
an unstarted preparation has no measured Usage or observed native Session identity.
A cancellation deadline cannot establish cleanup completion while cleanup remains
pending. This adapter ownership seam does not register a daemon capability or
supply per-Session placement authorization, public admission or an idle Files owner.

The optional private `agent.WorkspaceReader` on this preparation and transferred
Session requires the packaged `workspace_read` feature. It sends bounded relative
paths to that same SDK Query's native `readFile` control. Only the adapter combines
the path with the frozen workspace root; callers cannot replace the placement.
The pinned native read handler awaits file-handle close before its successful
base64 response. The adapter validates bytes and truncation, bounds each result
to 1 MiB and each request to 8 KiB, and admits one read at a time. Its continuous
bridge output consumer retains read receipts during preparation and across Start.
Caller cancellation detaches observation without cancelling the Run or discarding
an admitted waiter; its original deadline still applies. Owner closure stops
admission. Native null, malformed receipts, timeout and interrupted delivery remain
uncertain and stop the owner; local reap is not a successful read settlement.
The SDK's nullable result catches all native/control errors, so it cannot distinguish
missing files from denial or transport failure. This does not provide a public
Files endpoint, snapshot consistency, placement registration or idle owner policy.
The qualified live workspace fixture also checks binary, empty and bounded reads
before input and during real execution, plus effects before cancellation and reads
on fresh-process history continuation.

The optional private `agent.WorkspaceDirectoryLister` requires the Linux-only
`workspace_directory` bridge feature on the same preparation or transferred Session.
The pinned SDK has no directory-enumeration control; its fuzzy file suggestions are
not an inventory. This narrow adapter operation therefore reads metadata inside the
already qualified co-located mount/process boundary. It pins the configured workspace
root and opens each relative directory component with `O_DIRECTORY | O_NOFOLLOW`,
using `/proc/self/fd` paths anchored to held descriptors. It rejects directory symlink
traversal; `lstat` reports a symlink entry itself without following its target.
The fixed workspace policy requires canonical, disjoint protected roots and excludes
dynamic permission replacement and remote-workspace fallback. It does not implement
an additional permission engine or authorize an unqualified placement.

Directory requests are bounded to 8 KiB and 1,000 immediate entries, with explicit
truncation, literal names, kinds, and sizes only for regular files. They do not promise
ordering, snapshots, recursion, or public pagination. Missing and permission errors
are returned only from distinguishable filesystem outcomes; unknown results stop the
owner. Each operation closes its directory and intermediate descriptors before a
successful receipt; the root descriptor remains owned until bridge release.
Caller cancellation, Start transfer and owner shutdown retain the existing workspace
read settlement rules. This adapter gap fill alone does not enable public Claude Files; the dedicated
Runtime integration supplies public placement and ownership.

With `ObserveToolObservations`, private workspace execution requires the packaged
`workspace_command_observations` feature and emits the existing neutral command
snapshots. Match root, current-query native Bash call/result identities after input;
ignore historical replay, synthetic and child work. Preserve exact command text and
the native per-call textual result, including native rendering or truncation. This
is final native output, not incremental stdout/stderr or reconstructed interleaving.
Native error results are failed; unambiguous structured interruption is incomplete.
Missing results close as incomplete after the observation drain; query cancellation
does not overwrite an already observed native failure. Do not infer an
exit code from rendered text or supply cwd/duration without qualified native fields.
Preparation alone emits no command. Cold continuation must not reissue historical
observations. This private translation does not enable public workspace admission,
Read/Edit Items or Files ownership.

The private adapter also accepts typed anonymous HTTP and static-bearer HTTPS MCP
declarations on the trusted `environment:none` harness host. The packaged readiness
report must include `mcp_http_tools`; discovery advertises that feature only when
present, and execution
rechecks the installed bundle before dispatching an MCP request. An unchanged SDK
version alone cannot qualify an older bridge. Authenticated private requests also
require the packaged `mcp_http_bearer_auth` feature at discovery and dispatch.
The daemon generates a separate environment reference for each server and launch;
only those references enter the bridge request and native SDK configuration.
The native HTTP client expands them from its owned process environment. Literal
bearers must never enter SDK MCP headers because that configuration enters argv.
Readiness probes receive no per-request bearer environment. Token validation is
shared with the Codex adapter; credential storage remains an opaque-string contract.
Public Claude MCP admission reuses the shared resolver, immutable Session snapshots
and neutral Item/event projection.
The API checks the supported profile before persistence, during device selection
and again before claiming execution; a missing runtime capability leaves work queued.

MCP queries use the SDK's main-thread Agent definition to restrict model-visible
tools, in addition to empty built-ins, strict MCP configuration, empty setting
sources and default-deny permissions. Permission allowlists alone do not restrict
the native model inventory. Null selects all tools from a declared server; an empty
list selects none. Host functions compose with those selections. Native server
status supplies original tool identities; map their normalized native aliases while
preserving the original names in observations. Native status deduplicates aliases,
so it does not prove a complete original server inventory. A native PreToolUse hook
waits for inventory verification before admitting root calls and denies unverified,
mismatched or cancelled calls. The native Agent restriction controls model-visible
tools; inventory verification is not a barrier before the model request.
Anonymous HTTP declarations explicitly set an empty Authorization header to disable
native OAuth and automatic credential injection. Preserve that header; do not erase
native history or credentials to enforce this boundary. Servers that reject a blank
Authorization header, normalized name collisions and inventory changes during a
query require separate validation; this profile covers static inventories.
Private SDK status/control objects can contain expanded authentication headers.
Read only connection and tool identity fields; never retain, log or publish raw
status/configuration or control responses. Diagnostic projections must whitelist
safe fields. This does not permit filtering actual model/tool output to hide a leak.
The bounded adapter profile currently requires connected servers, reserves the
`functions` label, accepts alphanumeric/underscore/hyphen server labels and
alphanumeric/underscore/hyphen/dot selected tool names, and excludes remote
environments. Required startup is separately qualified by `mcp_http_required`.
All HTTP MCP queries use native SDK startup and an empty input iterator to
confirm initialization hooks. Required declarations additionally check connected
server status before the initial prompt is released exactly once. Pending, failed,
missing or ambiguous required status rejects before input; native startup timeouts are retained without
an adapter retry loop. Normal system/init still verifies Session identity and the
complete inventory before input readiness/tool authority. Optional servers retain
their existing inventory checks without a new pre-input connection requirement.
A Runtime must advertise the concrete required-initialization capability; there
is no fallback to an older execution path.

Public Claude static-bearer HTTPS MCP reuses the shared
Vault attachment, frozen selection and scoped decryption path. Selection and final
preclaim require the existing bearer capability; shared authentication dispatch
uses capability/placement checks rather than a Codex-name restriction. Missing keys
or failed lookup/decryption never fall back to anonymous execution; an attached
Vault with no matching credential may remain anonymous. These are execution limits,
not saved-Agent schema restrictions or changes to the official protocol.

Root assistant tool calls and live root user results produce the existing neutral
MCP observations. Correlate actual Session/call identities; exclude replay,
synthetic and subagent work and keep host function receipts separate. Preserve
the exact native `tool_use_result` when one result is unambiguous, otherwise the
per-call result content. Native errors remain observed native errors. The SDK can
replace annotated MCP content with rendered structuredContent and flatten MCP
errors; these observations do not claim original MCP envelope fidelity or hosted
output parity. Do not reconstruct lost fields or infer output from model prose.
Unfinished observed calls become incomplete on shutdown, without claiming that
remote tool effects were cancelled. Rich content, native truncation and asynchronous
MCP task results remain unverified.

Daemon `connect` optionally registers this factory as `claude_sdk` when the
operator sets `PARSAR_CLAUDE_SDK_ENTRYPOINT` to the absolute packaged `dist/main.js`.
`PARSAR_CLAUDE_SDK_NODE` selects Node (default: `node` on PATH). Discovery resolves
Node once and checks that exact configuration before pairing; the SDK's bounded
runtime check is independent of legacy CLI version probes. A ready SDK alone is
sufficient to start the daemon. No configuration means no SDK probe or descriptor;
failed readiness reports an unavailable descriptor with a rejecting factory.
Runtime checks establish local readiness, not provider authentication.

SDK state lives under `paths.ProfileDir(profile)/runtime/claude-sdk`, independently
of the replaceable runtime bundle. Both the entrypoint and managed state root must
be absolute. Background re-execution inherits operator configuration; it does not
persist provider credentials in pairing profiles. Product `claude_code` remains
unchanged. Product registration explicitly opts existing engines into
`WorkspaceAuthoring`; the authoring registry wraps only that opt-in. SDK registration
bypasses product capability-download, skill-upload and workspace-authoring wrappers.
It does not accept caller-supplied environment variables or business write authority.

The SDK descriptor advertises the validated daemon subset, including durable
Turns/input receipts, text observations, function tools, raw usage and restrictive
execution controls. It does not advertise permissions, product authoring, legacy
raw tool Items, general web-search control or text-verbosity levels. Router admission
for `environment:none` uses the available engine capability, not an engine name.
The independent API selects new Session engines through `AGENTS_API_ENGINE`
(`codex` by default, `claude_sdk` or `mcode`); existing Sessions keep their stored engine.
This remains the deployment default; the optional Core harness extension selects
an enabled engine for one saved or inline Agent configuration. API admission,
device selection and the final preclaim check share the execution service's narrow
engine policy without importing native adapters. Selected engines require the common
durable execution capabilities. Codex retains its general search/verbosity checks;
Claude uses its restrictive profile without claiming those general capabilities.
Idle and initial-input Session creation qualify the resolved configuration before
persistence; saved Agent resources remain independent of engine restrictions.
Claude additionally requires medium verbosity and explicit object-root function
schemas. Function-result batches normalize through the existing shared parser and
reject non-text content before any batch write, preserving pending calls and retry
identity. These are implementation limits, not changes to the upstream contract.
Do not bypass them by dropping fields, changing model identity or fabricating usage.
Operators may configure the daemon provider environment or the existing transient
`AGENTS_API_EXECUTION_OPTIONS_FILE` with adapter-owned `claude_provider`
(`base_url` HTTPS and `bearer_token`). Core forwards these opaque options without
persisting them in Session configuration. The adapter exclusively selects the
provider environment and removes credentials from native tool environments. Product `claude_code` and product execution are unchanged.
The `none` public profile accepts only
text, explicit model/system instructions, managed state, exact native resume and
declared functions with ordered text results, and the HTTP MCP subset
described above. It rejects unsupported request
options and disables built-in tools and undeclared MCP discovery.
`DisableExecutionEnvironment` and `DisableSubagents` are accepted assertions about
this fixed restrictive profile. Omission does not enable built-in tools. New and
resumed queries use the SDK's empty built-in tool set, explicit function MCP
configuration and allowlist, strict MCP configuration and empty user/project/local
setting sources. Without HTTP MCP declarations, native initialization and real
provider request inventories must contain only the declared host functions. Managed operator policy may further
restrict execution; it must not widen the profile. This limits model tool access,
not native state files or filesystem access by an explicitly supplied host function;
it is not sandbox/file isolation. The private factory accepts typed execution
controls only for disabled search and medium text verbosity. Search remains excluded
by the native tool inventory; medium retains the SDK's default text generation,
without adding instructions or changing caller input. The pinned SDK has no native
verbosity-level option: low/high and enabled search remain explicit implementation
gaps. Missing/invalid fields in a supplied control block fail before native setup;
omitting the block keeps the same restrictive profile. Public engine admission is
qualified separately by the API policy described above.
Use the SDK's history lookup before explicit resume; never fall back to a new
Session. Native files remain device-affine under a caller-selected managed
runtime directory. The launch configuration supplies trusted provider environment;
request options cannot supply environment variables or business write authority.
Omitted, null and empty `system_prompt` map to empty SDK instructions only at this
adapter boundary; null model values and unsupported options remain rejected.

The internal SDK function-server helper uses the maintained MCP server's public
request handlers and standard Tool/CallToolResult types. It snapshots definitions
and forwards JSON Schema without a JSON Schema-to-Zod conversion; supplied tools
are always loaded. Native call identity comes from the pinned harness's
`claudecode/toolUseId` MCP metadata, independently of request IDs, names or arrival
order. Missing identities and undeclared tools fail before invoking the host.
Return content/error fields unchanged over MCP and forward its per-request abort
signal. The private Go factory connects declared functions through this helper and
reuses the daemon function-call/result interface and opt-in neutral observations.
The native function-server registry must contain exactly those functions. SDK allowlisting admits
only these host callbacks; the host still owns result decisions and any business
permission checks. It grants no runtime-token business authority.

Function results remain pending after stdin/MCP delivery. A matching live, root
native user tool_result confirms application only when its Session/call identity,
error flag and returned text match the submission. Ignore replayed, synthetic and
subagent messages. Native error text joins the submitted text parts with newlines;
neutral observations retain their original order and separate failure status.
Missing/mismatched receipts fail the execution; do not replay unknown delivery.
Result submission waits at most ten seconds for a receipt and cancels uncertain
execution on timeout. Invalid or unsupported image results fail before consuming
a pending call. Function state belongs to one live Run and ends with it; the
existing router owns receipt retry/conflict handling. This does not establish
crash recovery or exactly-once effects. Public schemas outside MCP's object-root
contract and image result mapping remain admission/execution gaps.

Each SDK result supplies one native usage snapshot, including reported failures.
`Usage.Raw.claude_sdk_result` holds the latest; queries with multiple native results
also retain all snapshots in order under `claude_sdk_results`. Main-loop `usage`
is per native turn, while query-pipeline `modelUsage` and estimated `total_cost_usd`
are cumulative within the query. Retain subtype/error provenance and earlier
snapshots even when a later failure reports zero counters. Reuse the latest full
snapshot set in Usage and Done; never sum cumulative measurements. Each factory
invocation owns one SDK query, including cold resume, so no prior query counters
are carried forward. Missing native results do not imply zero consumption. SDK estimates stay
in raw evidence, outside the billed cost field; do not select an arbitrary model
or invent missing public token breakdowns. The API does not parse native counters.
Precise public usage projection, unreported costs and crash/partial accounting
remain gaps; the native snapshot alone is not complete protocol Usage compatibility.

Active text uses the SDK's `AsyncIterable<SDKUserMessage>` input, with a fresh
native UUID mapped to each daemon input ID. A native query may fold text into its
current native turn or queue another; one daemon Run can therefore contain several
native turns. Never promise Codex's same-native-turn semantics. Writes, queued
notifications and user-message echoes do not confirm consumption. Only matching
root assistant/partial/result `user_message_uuids` (or the singular fallback)
confirm applied input. Typed mid-turn folds may appear only on the native result.
Preserve that receipt even when the result reports failure. Check pending functions
after the query drains: the SDK may dispatch later-turn callbacks before the
earlier result handler finishes.
Keep the iterator open until every submitted input has a consuming result, even
when an earlier result reports an empty native queue. Close admission before
releasing final receipt waiters; drain and release the SDK/native processes before
one daemon Done. Cancellation resolves unconfirmed pending receipts as unknown and ends the
owned execution. Successful private SDK Cancel waits for owned-process exit and
stdout/stderr drain, then exposes the same settled CancellationOutcome as Done.
It retains native identity verified at input readiness, partial text and observed
Usage even on cancellation/failure; requested resume identity alone is not evidence.
A caller deadline before settlement reports failure/unknown, while cleanup continues.
Settlement precedes terminal publication so router completion cleanup cannot wait
on its own Done consumer. Cancellation releases intermediate event backpressure;
the settled outcome remains readable even when connection loss prevents publication.
A receipt timeout after a full write preserves the process and pending identity
without redelivery; a blocked write is cancelled and released.
The private adapter permits one input awaiting consumption and at most 63 extra
inputs per Run, preserving the native 64-UUID receipt bound. Durable receipt opt-in
separates bounded writes from native consumption waits; calls without it retain
the router's ten-second deadline. Larger input capacity and interrupted-input
recovery remain separate work. Daemon registration alone does not establish public acceptance.

Public text/function execution, active input, pending-call cancellation and cold
continuation are accepted for the registered restrictive profile. Environment
provisioning, broader tools/verbosity, complete public Usage, image results and
process-loss recovery remain gaps. Managed installation and release publication
remain separate tasks. `make check-cli` also builds
and tests the SDK package, including native output draining; CI selects that check
for changes to the package. Live adapter
acceptance is opt-in and must use a real provider with private credentials.

### Private Claude SDK runtime artifact

`make build-claude-sdk-runtime` exports the compiled bridge and pinned production
SDK/MCP dependencies, including the native package for the build host, into a
platform/architecture/libc-specific `.tar.gz` and SHA256 file under
`${PARSAR_HOME:-$HOME/.parsar}/build/claude-sdk-runtime`. `CLAUDE_SDK_BUILD_DIR`
may select another absolute output directory. The production dependency closure
requires Node20 or newer; Node22 is the tested version. Node is operator-supplied
and is not bundled; the bundle is independent of product sources, services and databases.
It does not add Node or SDK assets to the Agents API binaries/image.

The build validates source manifests with the repository-pinned pnpm frozen
install and compiles into fresh managed staging, never exporting incremental
checkout output. It then uses modern `pnpm deploy` with command-scoped workspace injection
and its dedicated frozen lock. The adapter has no workspace dependencies; keep
that boundary explicit. Do not enable injection globally or replace this with a
custom dependency copier. Export only compiled `dist` and production dependencies;
retain their package metadata, lockfile and licenses. Check dependency links stay
inside the export, pinned SDK/MCP/native versions, native `--version`, and bridge
startup before publishing the archive. Startup with stdin EOF is an import check,
not model execution acceptance. `make check-cli` includes this artifact check.

Extract the archive into a fresh managed runtime directory on a matching host
and use its absolute `dist/main.js` as the private factory entrypoint. Validate
relocation and real provider cancellation/continuation before accepting an
artifact. Linux x64/glibc with Node22 is the currently exercised platform;
other hosts require their own native acceptance. Do not reuse a bundle across
platforms or libc variants. Automatic Node installation, managed activation and
release publication remain separate work. Operator-configured daemon discovery/registration is supported as
specified above.

The exported `dist/runtime_check.js` companion is the local readiness contract.
It checks Node20+, installed SDK/MCP/native versions against the package manifest,
contained dependency resolution, native startup, and the exact `dist/main.js` bridge
with stdin EOF. It emits one versioned JSON report without calling a model or
creating Session state. The artifact check reuses this companion and separately
checks all exported links, the lockfile and source pins. `claudesdk.CheckRuntime`
uses the same operator-supplied Node, entrypoint and environment as execution,
with shared process-group ownership, bounded output and a 15-second deadline
plus bounded cleanup. Both native and bridge probes have five-second limits.
Return unavailable on failed or malformed probes; never forward native diagnostics
or treat local readiness as provider authentication, public capability acceptance
or filesystem isolation. Automatic installation remains separate.

### Agent knowledge references

- Unpublished knowledge retains only the bound version in other workspaces;
  automatic updates must not expose subsequent private revisions.
- `knowledge` capabilities store named UTF-8 reference documents in canonical
  versions. Reuse capability permissions, publication, versioning, and Agent
  bindings; do not add workspace-wide automatic injection or a parallel store.
- Accept pasted text and Markdown/TXT uploads (16 documents, 32 KiB per base).
  Names are labels, never paths to read. No fetching, conversion, RAG, or embeddings.
- Resolve enabled bindings for each run, honoring pinned/latest versions. Append
  a JSON reference-data block to the effective system prompt, including an
  explicit override. Keep Agent instructions and other capability semantics.
  Reject a combined reference block over 64 KiB rather than silently truncating.
- Codex resume must refresh developer instructions, including an empty string
  when removed. Unbinding prevents future injection but cannot erase documents
  already present in conversation history; use a new conversation for isolation.
- Knowledge inherits capability visibility. Publishing intentionally makes the
  documents readable to other workspaces; private documents remain private.

### Install and image freshness

- Configure product-to-Core access with `PARSAR_CORE_WORKSPACES_FILE`: each
  workspace maps to a distinct Core project and caller key file. See
  [product deployment](docs/deploy/product-core.md) for the complete configuration.
  Workspace model Provider credentials belong to the Parsar catalog and are sent
  through the write-only Core Session execution extension. Operator defaults remain
  an independent Core deployment option. An unconfigured product can start; Core operations return an explicit unavailable error.
- Root Compose deploys the product and its database. It mounts
  `PARSAR_CORE_CONFIG_DIR` read-only for workspace bindings and caller key files.
  The installer creates an empty binding list without overwriting existing
  configuration. The product does not start a runtime container.
- The installer pulls the server image, prepares the data directory for the
  image's actual UID/GID, verifies writability, then starts the product. Only the
  preparation container runs as root. It generates stable database/master keys.
- The installer is a thin Compose wrapper. Its options cover the installation
  directory, web bind/port, server image and validation. Native runtime history
  migration and sandbox image options have been removed; no legacy deployment
  compatibility or data backfill is provided by this cutover.
- Services exposed through a deployment platform may gain an ingress network,
  but they must remain explicitly attached to the Compose `default` network
  when they depend on internal service DNS names such as `postgres`.
- DB-driven workspace connectors exposed in the admin UI must start and stop
  from their persisted `enabled` and event-mode settings. Do not add a second
  deployment environment flag after a connector is saved and marked enabled.
- The root Compose file contains deployment infrastructure only. Do not add
  Feishu, Slack, Discord, or other workspace connector credentials or enable
  flags there; those integrations are configured through the web UI and stored
  in the encrypted connector tables.
- Avoid spelling out image, application, or Docker defaults in Compose. Keep a
  field only when it changes behavior, connects services, persists state, or
  exposes an intentional operator override.
- The local compose file must express the same default with
  `PARSAR_IMAGE_PULL_POLICY=always` for Parsar-owned images. Local image
  testing must opt out explicitly with `PARSAR_IMAGE_PULL_POLICY=never`.
- Local development images stay opt-in through installer overrides such as
  `--image parsar:local`; do not make
  local tags the default path for end users.
- The production server image must provide Node.js 22.20 or newer, `npx`, and
  `git` for server-side Skills.sh installs. Keep the runtime image compatible
  with the pinned `skills` package in `skills_install_routes.go`; the image
  build must fail if these executables are missing.
- Skills.sh downloads accept an exact `owner/repo` reference and a Skill slug,
  never a local path or a value the CLI could interpret as an option. Validate
  these inputs before invoking the installer.
- Skill directory packaging honors the request context while reading files and
  writing the archive. Enforce the ZIP parser's existing byte and entry limits
  during packaging, before upload or parsing can allocate oversized results.
- Skills.sh previews reuse the install downloader and Skill ZIP parser, require
  the same owner/admin permission, and create no capability, installation, or
  upload record. Temporary preview files stay under `~/.parsar/` and are removed
  after the request, including the downloader's own temporary files. Previews
  require Unix process-group cancellation so child downloads cannot outlive the
  request. Preview shows current repository content; installation
  fetches again and does not promise an immutable preview revision.
  Show the original SKILL.md entry alongside parsed metadata; do not reconstruct
  its source from canonical fields, which omit unsupported frontmatter.

### Paired-device companion CLI

- Device pairing installs `parsar-daemon` and the `parsar` companion CLI from
  the same server image or release. Stage both downloads before pairing; a
  missing CLI must not consume the one-shot pairing token.
- Pairing defaults to `~/.parsar/bin`; download-only mode retains its existing
  output-directory and non-executable behavior. Do not modify shell profiles
  or system directories. Upload-enabled tasks can find the executable `parsar`
  beside the daemon even after a later reconnect from a different shell.
- The server image and daemon release workflow ship both binaries for the same
  four platforms. Companion installation does not grant API authorization;
  task-scoped uploads keep the current run requester and workspace checks.

### Product Agent resource configuration

The V1 product definition lives in the [Agent configuration product document](https://vrfi1sk8a0.feishu.cn/wiki/TEhDwRlRpiGIcRkbKywcfEydn7g).
The Build inventory is Models, Environments, Capabilities, Integrations and
Credentials. Agent creation/editing selects those resources plus Harness. Product
resource selection and credential binding belong to Parsar; public protocol
features not yet implemented by Core remain explicit reservations, not alternate
product execution paths or changes to Core's contract.

- `resource_bindings` on Agent create/update contains version, pinning mode and
  per-resource configuration. Agent changes and the complete binding selection
  commit atomically. Omitting the field preserves bindings; an empty array clears
  them. Updating credentials preserves the selected version and pinning mode.
  Retaining an existing binding does not reapply new-install eligibility after
  unpublishing or deprecation; changing its version or tracking mode does. Retained
  foreign private resources never expose later private revisions.
- Capability bindings reuse `agent_capabilities` and Build imports. Model
  credentials default to the Provider key; an explicit
  `config.model_credential_binding` chooses a credential `kind` and personal/shared
  source. Model bindings accept only `openai_api_key` and `anthropic_api_key`;
  unrelated personal secrets must never become Provider tokens. Migrations register
  these model credential kinds without requiring development fixtures. Personal means
  the run requester, never the creator of a
  shared Agent. Public Agents require shared credentials. Existing per-capability
  choices take precedence over Agent-wide defaults. Missing references remain
  visible and require repair; lookup failures never substitute another credential.
- Inline resource creation keeps the Agent draft. Workspace communication
  connectors (Feishu, Slack, Discord and Teams) retain their shared scope and
  existing management APIs, with immediate-save scope stated in the Agent form.
  Dedicated Agent Feishu configuration remains owned by its existing API and is
  preserved when execution configuration is replaced.
- Skills verify the original owned archive and checksum, then adapt Build rootless
  ZIP layout and descriptive slug/title metadata to public inline Skill initialization.
  Supporting bytes and executable modes are preserved; native activation controls
  remain unsupported. System prompts and knowledge
  become protocol instructions. Core currently cannot combine a referenced
  Environment Template with additional Skills, or hosted execution with MCP;
  Plugins also remain unsupported. Preserve the Template selector and show these
  gaps. Do not silently drop a binding to start a partially configured Agent.
- Freeze Skill bytes and selected model credentials in the existing encrypted
  private Session snapshot, separate from the ordinary request. Restore the
  existing snapshot before reading mutable resources; decrypt initialization only
  for the Core request. Editing resources or credentials affects new Sessions.

### Product model catalog and execution credentials

Parsar owns workspace Model Providers, their write-only API keys, model catalog,
Agent model selection and business permissions. Reuse the existing `models` table
with workspace-owned Provider references; do not reactivate legacy product model
calls, probes or runtime adapters. Core has no product Provider CRUD or catalog.
Build → Models groups model rows under Providers using the Runs ledger pattern,
with Provider settings in the detail rail; Credentials remains for
third-party platform credentials. Members may read safe catalog metadata;
only workspace owners/admins may mutate it. Audit mutations without keys or input
payloads. Product keys require `PARSAR_MASTER_KEY` and authenticated workspace and
resource identity within the existing encrypted envelope.

An Agent saves `config.model_id` plus the exact resolved `model` string. The UI
selects from its workspace catalog and validates protocol/Harness compatibility.
Catalog identity and token limits are immutable; rename changes the display label
only. Anthropic Messages supports Claude Code and MiniMax Code; Responses supports
Codex. MiniMax Code requires explicit context/output limits. These checks establish
configuration compatibility, not availability of an arbitrary upstream model.

Before the first Core request, atomically freeze model identity and Provider settings
under the catalog row locks. Encrypt the private Provider snapshot separately from
ordinary `product_core_sessions.request`, bound to workspace and binding ID. Recover
an existing binding before consulting mutable catalog rows. Provider edits/deletion
and Agent edits apply to new Sessions only. Existing Sessions retain their model,
endpoint and key; key rotation does not rewrite them. A deleted selection fails new
execution explicitly. Omitted catalog selection on older Agents preserves their
existing operator-configured execution behavior.

Only Session creation carries the typed write-only execution configuration; the
product connector adds it after loading its durable snapshot. Core encrypts it in
the Session transaction using the existing credential cipher and tenant/Session
binding. Creation idempotency includes the confidential intent by hash. The native
adapters consume this snapshot without operator credential fallback. Missing keys,
decryption failure or incompatible configuration fail closed. Public resources,
events and ordinary configuration must not expose the key. Current support requires
a qualified hosted environment. See [Session model execution](contracts/agents-api/model-execution.md).

### Harness selection and Agent defaults

Core accepts the optional `agent.x_agents_core.harness` extension through the
saved Agent and inline Session configuration paths. Define the extension once in
`contracts/agents-api/v1`; never use metadata or a competing top-level selector.
Resolve saved overrides before selecting the existing Session engine, and apply
that engine's execution policy before persistence. Omitted selection preserves
the deployment default; explicit unavailable selection fails without fallback.
Effective extension reads use the persisted engine; Sessions without the extension
retain the official Agent response shape. See the [extension contract](contracts/agents-api/harness-selection.md)
for null/retry behavior and operator configuration.

Hosted engine-to-provider selection belongs to Core composition. Admission and
initial allocation share the mapping; retained allocations use their persisted
provider identity. Runtime images must satisfy their existing qualification rules.
Transient model options are partitioned by engine and must not expose another
engine's credentials. Do not infer an engine from a model name or template.

Parsar Agents save default model, harness extension and a typed, non-confidential
`config.environment` selector. The Agent form requires an explicit harness before
saving; existing records without one remain unset in read-only views. The Agent
list shows the configured environment type and harness in both table and compact
layouts. The environment selector is product configuration;
it becomes the official Session `environment`, never part of the protocol Agent.
Only a template reference or supported environment selector is stored, not live
container identity or initialization secrets. An explicitly chosen conversation
environment retains its existing override behavior. Otherwise the first message
uses the Agent defaults. Creating an Agent or opening an empty chat performs no
Core Session or container creation. One conversation/Agent binding freezes the
request on first execution; later messages and observer retries reuse it. Separate
conversations get independent Sessions and environments. Edits affect future
Sessions only. Keep the existing product navigation and direct empty-chat composer.

### Product Core execution

- Product Agent configuration uses the complete pinned inline Agent contract:
  model, instructions (mapped once from the product `system_prompt` field), tools,
  service_tier, reasoning, text and multi_agent. Do not narrow this schema to the
  current Core MVP. The explicit Core harness extension and product environment
  defaults follow the rules above. Reject old engine/device/provider settings; Core validates
  its current execution support. Business display/visibility stay in Parsar.
  A supplied product `config` replaces the execution configuration; omitted config
  preserves it. The separately supplied SP remains independent of that replacement.
  Inline Session configuration is the current integration, not a claim that product
  Agents are Core saved-Agent resources. See the [object mapping](docs/deploy/product-core.md#object-and-operation-mapping).
- Use workspace-specific Core clients with distinct project credentials. Never
  infer isolation from metadata, or fall back to a global key for an unknown
  workspace. Core verifies the configured project header. Keep the workspace's
  project stable across restarts; rotate only its credential. Configuration and
  deployment details live in [the product integration guide](docs/deploy/product-core.md).
- Core owns environment templates. Product routes authorize the workspace then
  forward official SDK operations to that workspace's Core project. Do not cache
  confidential template inputs in product tables or logs. The API forwards the
  full upstream schema; the UI identifies unimplemented initialization fields and
  does not present them as currently usable. Product Session metadata contains only safe environment
  selectors; initialization belongs to templates. Preserve Parsar’s sidebar and
  page-owned actions; do not duplicate navigation with upstream dashboard tabs.
  Docker/E2B are Core hosted providers, while official
  self_hosted requires its own executor connection. Missing key-management or
  connection workflows must show unavailable, not call private dashboard APIs.
- Persist one Core session binding per product conversation/Agent and one Core
  turn binding per product run. Freeze session requests, input and the preceding
  turn cursor before submission. Use stable product binding/run IDs as Core
  idempotency keys. A user retry creates a new product run; restarting an observer
  reuses the existing run and never creates another input event.
  Distinguish definitive admission rejection from uncertain delivery: Core's
  terminal environment-unavailable/expired/cancelled 409 codes fail and settle the
  product input. Other uncertain receipts retain recovery and the lane fence.
- Observe durable Turns and Items through the public SDK. Product SSE is a
  projection of these reads, not Core event-stream replay. Persist projected
  text/thinking/tool events before acknowledging them; restore their projection
  from product events after an interruption. Join function-call output items by
  `call_id`; a completed call alone is not its result. Preserve incomplete tools
  as unsuccessful in both live and persisted trace presentation. Recovered terminal outcomes retain
  their failure state even when the terminal event was already recorded. Persist
  measured usage idempotently before settling completed, failed or cancelled Core
  work; assistant-message creation is not a prerequisite for accounting. Neither service queries the other's database.
- Serialize each conversation/Agent lane with a PostgreSQL advisory transaction
  lock on a dedicated connection, independent of the product query pool. Check
  the connection while observing and cancel observation when the lease is lost.
  Each product process observes at most eight runs concurrently. Account for
  these additional connections when sizing PostgreSQL.
- Cancellation records durable product intent. Only the lane's current observer
  sends the session-wide Core cancel event and waits for the old turn to settle
  before admitting its successor. A delayed HTTP cancel handler must never
  directly cancel whichever Core turn happens to be current.
- Recovery scans queued/running runs and unsettled terminal runs at startup and
  periodically. Service shutdown stops observation without converting a running
  Core turn into a failed product run. Product database interruptions at invocation, Session/input binding,
  admission receipt, projection, usage or settlement boundaries are observation
  failures, not Agent failures. Leave them recoverable; never cancel accepted Core
  work because local persistence was temporarily unavailable. A deterministic
  invalid-member result before a Core binding exists fails product work instead
  of retrying forever. Frozen executions remain observable and cancellable
  after Agent
  retirement; retirement prevents new admission, not settlement of existing work.
  Deleting a conversation atomically cancels queued/running work. Internal Core
  cleanup reads retain access to submitted execution and its projection, including
  failed/completed product runs still awaiting Core settlement, while
  public conversation/run/event reads keep excluding deleted history.
  Auth, workspace membership, conversation ownership, IM, MCP
  access to Agents, scheduling, auditing and billing remain product concerns.
- The product currently supports text input, assistant text, reasoning summaries,
  tool observations, raw token usage and cancellation. Unsupported capability profiles,
  attachments, interactive approvals, application function results, local-device and sandbox
  administration are unavailable in this product client. Show this limitation in
  the Agent configuration page; reject unsupported input rather than silently
  ignoring it. A Core turn waiting for an unsupported interaction is cancelled
  and reported as unsupported.
- The product no longer exposes HTTP Agent invocation/worker/configuration,
  native streaming, model-provider administration, runtime pairing/credentials,
  sandbox lifecycle or in-place run requeue APIs. Reject retained legacy Agent
  connector types at web/IM/retry/scheduling admission instead of leaving queued work.
  Parsar retains its independent database, capability assets and versions, Skills
  import/upload, MCP configuration/OAuth, permissions and business orchestration.
  These are product asset operations, not runtime installation. Browser plugin
  extensions also remain product UI behavior. Runtime capability activation and
  loading must use Core; reserved unsupported bindings must display their execution
  limitation and fail before Session creation, never silently disappear.
  Asset import or OAuth success must never imply readiness for Agent execution.
  Landed migrations and retained business history are not rewritten or deleted.
- Soft-deleting an Agent preserves authorized conversation/run history and its
  identity. Deleted Agents must not accept new messages or retries. Explicit
  enable/disable and visibility changes retain actor-attributed audit records.
- `parsar-daemon` and `internal/agentdaemon` are execution-service infrastructure;
  their independent build and Core contracts remain in scope for their own tests.
  Do not reintroduce direct product-to-daemon execution.

### Agent CLI adapter contract

- MiniMax Code (`mcode`) uses native ACP over stdio. The default runtime pins
  the public `@minimax-ai/code` package; custom devices can override the binary
  with `PARSAR_MCODE_BIN`. Select the advertised Parsar custom provider/model
  explicitly; never fall back to the CLI account or native default model.
- Refresh mcode config, bound Skill archives and `AGENTS.md` under its managed
  state directory before every turn, including resume. Disable external Skill
  discovery. Reject combined instructions over the native 32 KiB limit and
  unsupported attachments rather than silently dropping context.
- Ignore ACP history replay during session loading. Translate current-turn
  text, thought, tool, permission and input events into the existing daemon
  protocol; persist its native session id through the standard Done metadata.
  ACP context-token occupancy and cumulative cost are not per-turn usage. Do
  not report them as token consumption or advertise usage accounting.

- Ordinary daemon-managed Codex sessions default to `approvalPolicy=never` and
  `sandbox=danger-full-access` on both `thread/start` and `thread/resume`,
  including conversations created under an older policy. When the operator sets
  `PARSAR_CODEX_PERMISSION_PROFILE`, the named native permission profile replaces
  that sandbox override while approval remains `never`. The daemon owns these
  defaults; no approval environment switch is required.
  Explicit engine approval requests still use the durable interaction lifecycle;
  user-input requests continue to wait for a human answer.

- Each Codex daemon Run owns the root Thread returned by its successful
  `thread/start` or `thread/resume` RPC and one active native Turn. Thread
  notifications cannot replace that identity. Filter notification-driven output,
  Items, Usage, steering and terminal state by the root Thread and available Turn
  coordinates before changing state, including when notifications precede RPC
  replies. Preserve the root resume Usage baseline before its Turn begins. This
  notification isolation does not implement child server-request interactions or
  enable public subagents.

- Active-turn text uses optional daemon `prompt_steer` / `prompt_steer_ack`
  frames, correlated by run ID and payload `input_id`. Check the engine's
  advertised `steering` capability first; Codex uses native `turn/steer` with
  its thread ID and active turn ID precondition. It must never start another
  turn as a fallback. Only a matching engine receipt confirms acceptance.
- During an active run, the daemon retains up to 256 steering attempts and
  replays their receipts. Reusing an input ID with different text is rejected;
  capacity exhaustion rejects new inputs instead of evicting receipts.
  `not_ready` / `busy` mean no input was sent and the same input can be
  retried without changing its identity or text. Native delivery runs outside
  the shared dispatch loop, with at most one in-flight input per run. An
  `in_flight` receipt means its result is still pending. A native error response
  yields `rejected`; a transport failure or invalid receipt yields
  `outcome_unknown`, cached without automatic redelivery even after an ack-send
  failure. A steering deadline or cancellation closes a blocked native RPC
  write; waiting for a receipt after a complete write must preserve the process.
  Unexpected transport exit ends the run with an error, without duplicating a
  terminal outcome already received. These receipts disappear with the run;
  durable recovery and interpreting missing receipts remain server-owned.
  This adapter contract does not expose the public Agents API events endpoint
  or change the existing product submission path.

- Agent cloning copies enabled capability bindings, version choices, and configuration.
  Pinned clones retain the stored version; latest choices resolve from the current catalog.
  Display, credential checks, and submission use the same version. Shared credentials remain
  independent per capability, and personal or unavailable credentials require a new choice.
- Agent creation and editing store behavior instructions in `system_prompt`.
  Preserve saved text when opening the form; clearing it sends an empty string.
- Property-only Agent edits omit the legacy `capabilities` replacement field
  unless the user operates its selection controls. That name list can lag
  canonical bindings; it must not reconcile bindings during unrelated edits.
- OpenCode model selectors use `provider/model`. Its Anthropic SDK base URL
  derives from the same endpoint resolver as model probes. OpenAI-compatible
  and OpenAI SDK adapters use the `openai` and `openai-response` endpoint maps,
  respectively, preserving explicit base paths; absent mappings preserve their
  legacy base URL. Explicit provider SDK options retain precedence over generated
  defaults.
- OpenCode usage records the model selected by the CLI launch plan, removing
  only the provider prefix. Without an explicit selection, keep the model unknown;
  do not infer it from token counts or change cost/provider semantics.
- Claude Code streaming deltas and their per-block assistant copies must be
  emitted once; preserve separate text blocks even when their content matches.
- Claude Code failure details may arrive in `error`, `result`, or `errors`.
  Preserve supplied details before falling back to the result subtype.
- Codex app-server usage totals are cumulative per thread. Treat restored usage
  before `turn/started` as the baseline and persist only the current turn's
  delta; repeated snapshots must not increase recorded usage.
- Codex cancellation sends both native thread and Turn IDs. Capture the observed
  Turn identity while stopping steering; before a Turn identity is known, send the native
  explicit-empty startup Turn ID. Do not interrupt an already observed terminal
  Turn. The existing two-second interrupt budget covers both the control write
  and response wait. A blocked write closes only its owned transport through the
  shared deadline-aware writer, then uses existing process cleanup. Cancellation
  remains best-effort: a completed write or applied daemon receipt does not prove
  remote interruption, descendant exit or a global daemon shutdown deadline.
- Codex RPC Close initiates stdin closure and bounded kill escalation once. Each
  call waits for the same owned child to be reaped; a timeout wraps
  `context.DeadlineExceeded`, and later calls can resume waiting. Success means
  local child reaping (or no child was spawned), regardless of its exit code.
  It does not acknowledge remote executor retirement or descendant cleanup.
- Omitted Codex mode means `default`, matching the Agent UI. Send the current
  instructions through that turn mode; cold resume alone may retain old instructions.
- OpenCode JSON CLI tool parts arrive after execution. Translate each terminal
  call once into paired tool-call/result records for existing trace consumers.
  These records describe completed work; they are not permission requests or
  measurements of the original tool duration.
- Every daemon-side agent adapter must use a shared process runner for CLI
  subprocesses. New adapters must not hand-roll separate `Start`, stdin,
  cancellation, timeout, and `Wait` loops.
- Every subprocess must be waited/reaped. Cancellation must close stdin when
  appropriate, send a graceful signal first, and escalate to kill after a
  bounded timeout.
- A completed prompt closes its protocol stream immediately, but daemon-side
  CLI processes and their background children stay alive until the
  conversation has received no new prompt for one hour. A new prompt for the
  same `AgentStateKey` renews that idle window. Explicit cancellation, device
  shutdown, and daemon shutdown still terminate processes immediately.
- Run terminal-state persistence must use a short independent context. A
  dispatch deadline or cancellation may stop connector work, but it must not
  prevent the server from recording the resulting completed, failed, or
  cancelled state.
- Once cancellation is persisted, late connector completion/failure events must
  not create conflicting run lifecycle records. Preserve nonterminal diagnostic
  events and existing history; cancellation owns the terminal outcome.
- Manual run retries create a new Run ID through `/agent-runs/{runID}/retry`.
  Preserve the source run's terminal status, events and output; reuse its trigger
  message, and execute as the current requester. One source run maps to one retry
  (`retry_run_id` / `retry_of_run_id`), so repeated requests cannot create duplicate
  attempts. Dispatch only after commit, independently of HTTP cancellation. Keep
  the legacy same-ID `/requeue` endpoint separate from this user-facing workflow.
- When an engine supports resume, persist the upstream session id through
  `agent_engine_sessions` and pass `AgentSessionID` plus `AgentStateKey` over
  the daemon protocol. Do not keep resume ids only in adapter memory, files
  without a server record, or frontend state.
- Adapter-specific state directories must be derived from `AgentStateKey`
  under `~/.parsar/`; never use the repo checkout, container image working
  directory, or the process CWD as hidden state.
- Keep uploaded Skill archives engine-neutral. Materialize adapter-managed
  copies below the `AgentStateKey` runtime directory, then register that root
  through the engine's native CLI, config, or RPC surface.
- In-process Skill and Plugin installs serialize cache checks, extraction, and
  pruning for the same install root. Waiting honors cancellation; independent
  roots remain concurrent and idle locks are released. All adapters use the
  daemon's shared `internal/agent/installroot` coordinator.
- Markdown Skill imports and new versions must store an engine-neutral ZIP
  containing `SKILL.md`, with its storage reference and SHA-256 persisted before
  reporting success. Existing versions without archives require a new import.
  Generic capability creation may create Skill metadata, but Skill versions
  must use the import commit endpoints; generic version writes reject them
  rather than storing a version the runtime cannot load.
- Skill ZIP preview and commit must reject duplicate paths, including
  normalized separator/dot-segment and case aliases, so approved file contents
  cannot differ because an extractor chooses a different duplicate entry.
  Reject filenames outside Unicode stream-safe normalization rather than
  adding a separate unbounded normalizer.

### Runtime Skill uploads

- `parsar plugin add` may upload inline Skill bundles using a per-run
  `PARSAR_CAPABILITY_UPLOAD_TOKEN`. The daemon supplies its paired server URL
  per request; never export the device's runner credential to an Agent shell.
  The default sandbox includes the CLI; custom runtimes must install it separately.
- The upload endpoint derives workspace and actor from the persisted run's
  requesting user, checking current owner/admin membership and running status
  on every request. Tokens expire after one hour and are unusable once the run
  ends. They do not authenticate workspace management or other runtime APIs.
- This path creates workspace-only bundles of inline Markdown Skills. It cannot
  publish publicly, bind Agents, upload supporting files, or install server/client
  plugin code, hooks, tools or credentials. Existing FDE APIs remain independent.
- Shared/cloud runtimes remain Agent-owned; uploading does not assign a fixed
  user to the device or change spec/memory identity rules.

### Daemon workspace authoring

- Workspace authoring uses the existing reverse daemon WebSocket. Both request
  and response keep `Envelope.ID` equal to the active run ID; `request_id` only
  correlates a command. The run's authenticated subscriber owns the operation,
  and the server derives workspace, Agent and requester from its persisted run.
- Commands run with bounded concurrency and cancellation tied to the run stream;
  their database or archive I/O must not block consumption of lifecycle events.
- The companion CLI connects through `PARSAR_DAEMON_SOCKET`, a per-run Unix
  socket under `~/.parsar/authoring/` with mode `0600`. The daemon closes it on
  turn completion or cancellation, even while retaining the engine process.
  It does not need a public server URL or a user/device bearer credential.
  Shared OS identities are for trusted FDE workloads; use separate runtimes
  when Agents require isolation from each other's local processes or files.
- Engines advertise `workspace_authoring` in their heartbeat capabilities.
  Only compatible daemons receive the authoring flag and command instructions.
  The existing inline bundle upload credential and CLI remain compatible.
- Every command requires a running user-requested task and current workspace
  membership. Writes follow the existing owner/admin policy. The allowlist is
  workspace context, Skill list/read/create/new-version, and read/replace of the
  current Agent's system prompt. Do not add arbitrary routes, caller-supplied
  principals, public publishing, credential access or automatic Agent bindings.
- Skill writes reuse the canonical Markdown parser, stored ZIP and transactional
  capability/version import. The first release writes single-file Skills and
  rejects updates to public Skills or updates that would discard supporting
  files from the stored archive. Preserve original Markdown, including unknown
  frontmatter, in the stored archive and return it on reads. Existing audit records
  retain the requester; the import source records the originating run ID.
  Version writes recheck workspace visibility and the inspected latest version
  under a capability row lock, rejecting changes made during archive I/O.
  System-prompt writes use the existing partial Agent update and apply next turn.

### Plugin Bundle (KindBundle) architecture

- A Plugin Bundle is a `KindBundle` capability that packages server tools,
  client UI, skills, and hooks as a single deployable unit installed via
  `parsar plugin add`.
- **Server tools** run inside `server/plugin-host/` — a Node.js process
  speaking MCP stdio protocol (JSON-RPC 2.0). The daemon spawns it like
  any other MCP server (`{ command: "node", args: [...] }`).
- The plugin-host process is configured via `PARSAR_PLUGIN_HOST_PATH` (env
  var pointing at `server/plugin-host/index.js`). When unset, bundles with
  `server_entry` are silently skipped with a log warning.
- Plugin server code lives on disk at `<PARSAR_DATA_DIR>/plugins/<dir>/`.
  The CLI copies files during `parsar plugin add`; the server reads them
  at prompt time via the plugin-host `--plugins-dir` argument.
- Directory names strip the `@scope/` prefix from bundle names
  (`@internal/hotel-ops` → `hotel-ops`). This logic is duplicated in
  `apps/parsar/internal/cli/plugin.go` (`pluginDirName`) and
  `server/internal/connector/agentdaemon/capability_runtime.go`
  (`bundleNameToDirName`) — keep both in sync.
- `resolveBundleCapability` returns a `bundleResolution` struct containing
  both system prompt injections (skills) and MCP server configs (tools).
  The MCP server name is `"plugin:<bundle_name>"`.
- Plugin SDK (`server/plugin-host/lib/sdk.js`) provides
  `ctx.tools.define(name, { description, parameters, handler })`. Future
  phases will add `ctx.hooks`, `ctx.credentials`, and `ctx.api`.
- Plugin tool handlers have a 30-second timeout. Errors are returned as
  MCP tool-level errors (`isError: true`), not JSON-RPC errors.
- **Client UI** uses a slot-based extension system
  (`apps/web/src/lib/plugin-slots.ts`). Plugins register React components
  to named slots via `ctx.slots.register(slotId, { key, component, match? })`.
- Slot types: `single` (last registration replaces), `list` (all render
  in order), `chain` (first match wins — used for tool-card rendering).
- Client bundles are built by the CLI during `parsar plugin add` using
  esbuild (`server/plugin-host/build-client.js`). Output goes to
  `<plugins_dir>/<name>/dist/client.js`. Served via
  `GET /api/v1/plugins/{name}/client.js`.
- React is shared via `window.__PARSAR_PLUGIN_API__` (exposed in
  `plugin-init.ts`). Plugins must NOT bundle their own React.
- Plugin client bundles use IIFE format with a `require()` shim and an
  esbuild `externalize-react` plugin. Standard `import React` works;
  `react-dom` specific APIs (`createPortal`, etc.) are not yet supported.
- The frontend loads plugin clients on page load via `usePluginClients`
  hook. Binding/unbinding a capability triggers an immediate reload
  through React Query invalidation.
- Predefined slot IDs (add new ones as FDE needs arise):
  `workspace.main`, `workspace.content`, `layout.header.actions`,
  `layout.nav.bottom`, `conversation.tool-card`,
  `conversation.header.actions`, `conversation.input.dock`,
  `conversation.composer.left/right`, `agent.workspace`,
  `agent.settings.section`.
- Adding a new slot point: wrap the target area with
  `<SingleSlot slotId="..." fallback={<OriginalContent />} />` or insert
  `<ListSlot slotId="..." />` at the desired position. Each new slot is
  3–5 lines of code.

### Human interaction lifecycle

- Interaction reads expose the run's recorded requester type and ID. Resolve
  current user names only through active membership in that interaction's
  workspace; missing names retain the recorded identity. Web decision surfaces
  share operation and argument presentation without inferring unreported scope.

- `agent_interactions` is the canonical durable record for permission prompts
  and `AskUserQuestion` / `requestUserInput` requests. The Web approval inbox,
  conversation SSE notices, and IM cards are presentation surfaces over that
  record.
- The active Web conversation renders its pending durable interactions as full
  decision cards. SSE request IDs may prioritize a newly emitted card, but the
  workspace interaction query must restore the card after refresh. The inbox
  remains the workspace-wide queue, and both surfaces reuse the same decision
  component and resolution API.
- `conversations.metadata.gateway_inflight.permission` and
  `prompt_for_user_choice` remain channel delivery slots only. Do not make a
  runtime response depend on an IM card having been rendered first.
- A daemon adapter that supports approval or user input must emit the shared
  protocol request and defer the engine response until
  `SubmitPermission` / `SubmitPromptForUserChoice` arrives. Adapters must not
  silently approve, deny, or synthesize empty answers as a fallback.
- Codex MCP empty-form confirmations use the same permission lifecycle and
  reply with MCP action/content fields. Decisions grant only that call; structured
  forms and URL elicitations remain unsupported rather than implicitly approved.
- Codex agents that may call `request_user_input` use `config.mode=plan`.
  Prompt wording cannot unlock the tool in default mode; the daemon must pass
  the configured mode through app-server `turn/start.collaborationMode`.
  Agent create/profile configuration owns this persisted value, accepts only
  `default` or `plan` for Codex, and clears it when switching engines.
- Daemon-originated approval and question envelopes keep `Envelope.ID` equal
  to the run ID so the server can deliver them to the active subscriber. Put
  the daemon-minted interaction handle in payload `request_id` / `ask_id`;
  legacy permission IDs in `Envelope.ID` are read only for decision-routing
  compatibility.
- Persist the run event and its derived `agent_interactions` row in one
  transaction before publishing an approval or question to SSE/IM surfaces.
  If that canonical write fails, abort the run instead of exposing an
  actionable card that cannot be claimed or recovered.
- Web and IM responders must call the same interaction resolution service.
  Routes and card callbacks must not implement their own status transition or
  deliver to the runtime before the canonical compare-and-swap claim succeeds.
- Question answers use the stable question ID from the adapter and preserve
  selected values as an array. Headers and positional answers are compatibility
  fields only; they are not durable identity.
- Preserve adapter question metadata end to end. `is_other=false` forbids a
  free-text answer, `is_secret=true` uses a masked input wherever that surface
  collects free text, and secret answer values may travel to the waiting
  runtime but must be redacted from
  `agent_interactions.response`, interaction-resolution run and audit events,
  logs, API reads, and rendered chat receipts or callback summaries.
- Every deferred request has a bounded lifetime. The server expiry worker is
  authoritative: it explicitly denies a permission or cancels user input,
  unblocks the runtime, and leaves an `expired` terminal record. Daemon timers
  are a safety net and must make the same deny/cancel choice. Neither path may
  silently continue the requested action.
- A server-to-daemon WebSocket write is not proof that the engine accepted a
  decision. The daemon must return an application-level decision ack after
  `SubmitPermission` / `SubmitPromptForUserChoice` succeeds; only then may the
  server persist the terminal interaction state. Missing or negative acks keep
  the canonical interaction retryable (except a definitive `not_pending` or
  replay `decision_conflict`, which closes it as runtime-gone).
- Every transport attempt uses a unique delivery ID so a late ack cannot
  satisfy a newer resolver. Daemon replay is keyed by runtime request plus the
  decision payload with that delivery ID excluded: identical retries are
  acknowledged without a second apply, while changed retries conflict.
- The decision-ack wire contract starts at agent-daemon protocol `0.2`.
  Server and daemon keep the existing strict major/minor handshake so a `0.1`
  peer fails closed and must be upgraded instead of applying an unacknowledged
  decision during a rolling-version mismatch.
- Human responses are workspace-scoped, reject viewer writes, claim a single
  winner before contacting the runtime, persist a terminal state, clear the
  matching inflight slot, and emit an approval audit event. A terminal run
  cancels any still-open interactions. Multi-pod daemon routing resolves
  `request_id` through the canonical interaction's `device_id`; IM slots are
  only a legacy fallback.

### Execution environments

Execution environment acquisition, lifecycle, native session state and sandbox
images are owned by Core. See [Environment ownership and placement](#environment-ownership-and-placement)
and the independent execution artifact contracts above. Product deployment must
not mount the Docker socket or provision runtimes on an Agent's behalf.

### API, DB, and generated surfaces

- Workspace run search matches run IDs, Agent names/slugs, and conversation IDs
  as case-insensitive literal text before pagination. Page rows and totals use
  the same search, status, and workspace filters.
- Skills.sh installed state is read from persisted version provenance within
  the workspace, including older versions and deprecated capabilities. Deleting
  the capability removes that installed state; names are not registry identities.
  Refresh it on directory entry and after installation; mutation responses do
  not establish the current installed mapping.
- Secret disabling is authorized by `secrets.management_workspace_id`, set
  from the creation workspace. This ownership must not restrict shared reads
  or runtime use. Legacy rows inherit unambiguous creation metadata or runtime
  registration ownership. Rows without it remain usable but cannot be disabled until
  an operator assigns a verified management workspace in the database.
  Never infer ownership from the workspace supplied in a disable request.
  The database smoke gate also runs the cross-workspace secret-disable HTTP
  regression so it cannot silently skip in CI without a test database.
- Accepting an invitation may set a password only for a newly created user.
  The saved invitation name initializes that user's name; an empty name keeps
  the email-prefix fallback. Existing account names are never overwritten.
  Existing users must authenticate as the invited account; acceptance must
  preserve their identities and passwords, and rejected attempts must leave
  the invitation available for its recipient.
- New persistent state starts with a migration and sqlc query. Avoid direct
  SQL embedded in route handlers or connector code unless the package already
  owns that persistence boundary and tests cover it.
- JSON config blobs are allowed only at integration boundaries where providers
  are genuinely schemaless. Once two call sites read the same key, introduce a
  typed parser/normalizer and make all callers use it.
- Frontend API shape mirrors must live in `apps/web/src/lib/` next to the API
  client/hook that owns them. Page components should receive typed values, not
  parse runtime/provider/config JSON themselves.
- Multi-endpoint model rows keep the default/legacy `models.base_url`, but
  protocol-specific runtime URLs belong in `models.config.endpoint_base_urls`
  keyed by `supported_endpoint_types` values such as `anthropic`, `openai`, and
  `openai-response`. Runtime injectors must consult the endpoint map before
  falling back to `models.base_url`.
- Generated files (`docs/openapi/openapi.yaml`, `server/internal/db/sqlc/*`)
  are committed artifacts, but never the source of truth. Change annotations
  or SQL first, then regenerate.

## Code quality & architecture

Prefer existing code, official SDKs and maintained third-party components before
adding custom infrastructure. Keep adapters limited to product-specific behavior;
pin dependencies and verify compatibility at the service boundary. Record the
chosen dependency and any necessary custom implementation in the linked task.

Parsar favors small, single-purpose files and reused helpers over growing
files and copy-pasted logic. These rules are forward-looking: they do not
require immediately splitting existing large files, but any PR that adds
substantial new code to one of the files named below as an example must
split relevant pieces out first rather than growing the file further.

### File and function size

- Go: a source file crossing ~500 lines is a signal to split by
  sub-concern before adding more code to it. New files should stay under
  this from the start.
- React/TS: a component file crossing ~400 lines is a signal to extract
  sub-components/dialogs into their own files.
- What not to imitate: `server/internal/dev/routes.go` (6300+ lines),
  `server/internal/store/store.go` (8400+ lines),
  `apps/web/src/pages/admin/AgentsPage.tsx` (2000+ lines, ~40 top-level
  functions/components in one file).

### Package and file cohesion

- A package/directory groups one domain concern. `server/internal/dev`
  currently mixes auth, capabilities, uploads, scheduled tasks, RBAC, and
  sandbox admin in one flat package — do not add another unrelated route
  group there. Give a new domain its own file at minimum, and its own
  subpackage once it needs more than ~3 files or crosses ~800 lines.
- Store methods belong grouped by entity, not accreted into a single
  `Store` file/struct — see `server/internal/store/store.go` as the file
  not to imitate.

### No duplicate logic

- Before writing a formatter, parser, validator, or error-mapping helper,
  grep for an existing one. Reuse or extend it rather than writing a
  second `formatDuration` / `parseXID` / `writeXError`.
- If the same 3+ line pattern appears at a second call site, extract it
  before a third copy is added.
- Known helpers to reuse rather than reinvent: `decodeJSONWithField` /
  `decodeJSONWithFields` (`server/internal/dev/routes.go`) for JSON body
  decode errors; `parseLimit` / `parseOffset` (same file) for pagination;
  `apiRequest<T>()` (`apps/web/src/lib/api-client.ts`) for all HTTP calls
  from the web app — do not hand-roll `fetch`.

### Error-handling contract (Go handlers)

- One error-response helper per API surface, not a new sentinel→HTTP-status
  switch per file. `server/internal/dev/` currently has 6+ near-duplicate
  mappers (`writeRBACError`, `writeCredentialKindError`,
  `writeCapabilityError`, `writeImportParseError`, `writeReadError`,
  `writeStoreAgentError`) — new handlers must reuse an existing mapper for
  their domain instead of writing a parallel one, and must not inline an
  ad hoc `switch { case errors.Is(...) }` in the handler body.

### Usage attribution

- Managed daemon runs record the provider type from the same successful model
  resolution that builds the prompt options. Never re-read the catalog when usage
  arrives. Preserve adapter measurements and raw fields; `raw.parsar_usage` records
  `agent_kind`, `reported_provider`, and `provider_source` (`managed_model` or
  `adapter`). Unmanaged runs retain adapter provider labels, and completions with
  no reported usage remain empty. Historical rows are not inferred or rewritten.

### Frontend shared logic

- Editing a capability's credentials from Agent Config updates only that binding's
  credential choices. Preserve its stored version, pinning mode, and other
  configuration; resolve per-binding choices before Agent-wide defaults.

- Capability usage views distinguish loading, failed, and successful empty reads.
  Failed reads offer retry; incomplete Agent binding counts remain unknown. Query
  status must update even when a failed request leaves cached data unchanged.
- Credential deletion previews report unknown impact when workspace or capability
  reads fail, and offer retry. Do not present partial counts as a complete scan;
  keep the credential deletion operation independent of these advisory reads.

- Answered interaction cards display persisted `response.answers` by question ID
  (or `q{index}` fallback), rather than local drafts. Inbox and conversation cards
  share the answer projection; custom secret answers retain password masking.

- Usage cost displays treat stored zero/missing values as unknown: the current
  contract cannot distinguish free usage from unavailable pricing. Preserve raw
  records; sum finite positive costs only and label incomplete totals. Do not
  estimate prices or infer cost availability from provider names or token counts.

- Ordinary toasts expire even while hovered and provide a localized close
  button. Keyboard focus inside pauses their countdown until focus leaves.
  Persistent action prompts remain owned by their caller. Toast removal must
  have a timer fallback when exit animation events do not fire.

- Partial bulk model deletion keeps submitted names and per-item results in a
  dismissible dialog. Link references only after confirming the Agent in the
  authorized workspace directory; preserve reference text when lookup fails.
- Audit surfaces share readable action and actor labels. Resolve names only
  from workspace-authorized member/Agent reads and the recorded actor type;
  missing names or failed lookups retain the raw identity. Display names are
  current directory values, not reconstructed historical identities.

- Marketplace-to-Agent installation uses the same capability version and
  credential confirmation dialog as Agent configuration. Keep pending install
  intent in the existing route until completion or cancellation; clear it before
  showing the installed Agent configuration. Confirm a marketplace capability
  using its published version metadata; its source workspace version history is
  not a cross-workspace read API. Do not duplicate credential rules.

- Shared run queries refresh queued/running records until the server returns a
  terminal state. Terminal events may arrive later: allow up to 30 seconds of
  bounded catch-up reads until the matching event appears. Terminal details and
  lists without active runs stop periodic polling.
- Agent management opts into disabled records with a separate query-cache key.
  Ordinary Agent selectors keep active-only reads; status mutations invalidate
  both list variants and the detail before their pending state ends. Agent status
  operation state and feedback belong to the management page, above its selected
  detail and rail/modal presentations. Serialize status submissions until the
  current operation settles; changing details cannot drop an in-flight outcome.

- Tool-result failure is independent of the run's final status. Live and
  persisted tool views share the explicit result-failure predicate; ending a
  tool event does not itself imply success. Do not infer failure from prose.
- Inbound messages with `sender_type=external` are user turns. Web conversation
  presenters must not assign them the Agent identity or imply they are from the
  current viewer.
- Cross-page utilities (date/time/duration formatting, status labels,
  etc.) live once in `apps/web/src/lib/`. Do not reimplement inside a page
  component "because it's just a few lines" — that is how
  `RunsPage.tsx`'s `fmtDuration` and `AgentsPage.tsx`'s `durationMs` /
  `formatDurationMs` diverged into two slightly different
  implementations.
- `packages/ui` / `packages/core` are reserved for logic shared across
  more than one app. Until they are populated, shared web-only logic
  still belongs in `apps/web/src/lib/`, not duplicated per page.
- Global theme state lives in `apps/web/src/lib/theme.tsx`; page components
  must not read or write theme `localStorage` directly. Light/dark styling
  must flow through semantic tokens in `apps/web/src/style.css`, not per-page
  raw colors or duplicated `dark:` branches.

### Testing granularity

- When a function or file is split for the reasons above, its test moves
  or splits with it. Do not keep appending to an already-large `_test.go`
  (e.g. `routes_test.go`, `store_test.go`) for newly extracted code — give
  the new file its own scoped test file.

## Web UI hard rules

Standalone shared conversations size to the viewport, including loading and
unavailable states. Keep their mobile sizing scoped to that shell; the desktop
console's minimum width and shared message behavior remain independent.
Below the small-screen breakpoint, theme and composer action buttons in this
shell have at least 44px touch targets without changing desktop control sizes.

The console and shared conversation view use the same thread scroll hook.
Opening a conversation and successful sends follow the latest content. Scrolling
back or choosing a turn preserves the reading position during streaming and
polling; returning to the bottom resumes following.

Before a running conversation emits text or tool activity, its trace identifies
the wait and shows elapsed time. After 30 seconds, explain the option to wait or,
when permitted, stop; hide that guidance on output, human interaction, or termination. Do not
infer engine setup or model-request stages without corresponding events.

Dialogs / drawers / modals and detail panels **must not show a horizontal
scrollbar**. End users report "I can't see the bottom" far more often than
"my screen is too narrow", and horizontal scroll almost always means a
layout bug has leaked through — it is rarely intentional design.

Three concrete rules:

1. `DialogContent` defaults to `overflow-x-hidden`. Vertical overflow uses
   `max-h-[calc(100vh-2rem)] overflow-y-auto`.
2. Every `<pre>` / `<code>` block defaults to `whitespace-pre-wrap
   break-all` — code / JSON / shell commands should wrap, not force the
   user to scroll sideways. Exception: append-only terminal log streams may
   keep `overflow-x-auto`, but only when nested inside an
   `overflow-hidden` parent so the scrollbar can't escape the dialog.
3. Reader-facing error / warning copy uses `overflow-wrap: anywhere` to
   preserve normal word boundaries while containing long unbroken strings.
   Verbatim technical details keep the code-block wrapping rules above.

When a dialog uses a multi-column grid, give every column `min-w-0` —
otherwise long children push the grid track wider instead of wrapping.

Authentication submission failures use the shared `ErrorDialog`, preserving
form input and restoring action focus on dismissal. Field validation stays
inline; sign-in-required invitation guidance stays visible as a guided step.
The authenticated root owns post-login return navigation for password and SSO
sign-in. Preserve allowed in-app paths, query parameters and fragments through
the existing session return intent; login forms must not race that navigation.

`PageHeader` keeps its title readable and wraps actions when their combined
width exceeds the available panel width. Its minimum height remains 64px;
wrapped rows grow naturally. Keep this behavior in the shared header, with
`actionClassName` reserved for page-specific action arrangements.

Agent forms edit the model, instructions and pinned upstream Agent configuration.
Environment templates belong to Core; Agent forms save a default template or
explicit environment type for new Sessions. Preserve drafts when an upstream operation fails
and show unavailable features without inventing a successful local substitute.
New and cloned Agents default invocation scope to workspace. Scope choices
explain the Feishu gate without implying anonymous Web/API access.

Use `EmptyState` with `size="compact"` for detail tabs, subsections, and
compact result panels. Keep their alignment and spacing in that shared
component; page-level empty states retain the default size.

Structured `Ledger` columns own both header and cell alignment: numbers align
right; text, identifiers, and dates align left. Keep widths and alignment in
the shared column model, not separate page-specific header rules. Direct cell
components must forward `className` to their grid item. Fixed icon
tracks, legacy string templates, and rows spanning multiple columns retain
their existing layout.

When a list needs a compact presentation beside a detail rail, use container
queries against the list width. Keep the same data and row actions, with
explicit field labels when column headings are hidden.
Declare row actions with `col.actions(count)` so the shared column model reserves
space for the maximum visible button count. Hover and keyboard focus reveal the
controls within that space; they must not cover content or shift column widths.
Headers and rows retain the grid's minimum width when the list viewport is narrower.
For wrapping row titles, center status icons inside a one-line-height wrapper
(`h-lh items-center`), aligned to the first line rather than the whole text block.
The shared Ledger preserves the activated row's viewport position when its
width changes; after manual scrolling it anchors the current reading position.
Keep this behavior independent of routing, selection data, and detail focus.

Property groups use the shared `PropertyList` label track: 8rem capped at 40%
of the available width. Labels and plain text values wrap, including long
unbroken identifiers, with matching first-line spacing. Custom value controls
retain their own overflow behavior. Do not size groups from their longest label or add
page-specific label widths; groups in the same context must align.

Single-choice form controls use the shared Radix-backed `Select` and
`SelectOption` components. Pass values through `onValueChange`; do not fabricate
DOM change events. Keep option values, labels and disabled rules in the caller,
and popup styling, keyboard navigation and focus behavior in the shared control.
Keep its Radix dismissal and focus dependencies compatible with `Dialog` so
nested menus share one layer stack.
Associate each control with its field label through `htmlFor`/`id` or an
accessible-name attribute; include the item context for repeated selectors.

Persistent execution failures use `ErrorState` with `appearance="panel"` to
separate recovery guidance from expandable technical detail. Keep raw reasons
in one place; historical conversation messages disable live announcements.
Loading errors and field validation retain their existing presentation.

## Typography contract

The type scale has 7 defined steps. Arbitrary pixel sizes (`text-[Npx]`) are
banned by ESLint and will fail `make check`.

| Utility     | Size   | Usage                                    |
|-------------|--------|------------------------------------------|
| `text-xs`   | 12 px  | Badges, micro-meta, table footnotes      |
| `text-sm`   | 13 px  | Default body in dense admin UI           |
| `text-base` | 14 px  | Form labels, buttons, inputs             |
| `text-lg`   | 16 px  | Card titles, dialog headings             |
| `text-xl`   | 20 px  | Section headings, sub-page titles        |
| `text-2xl`  | 22 px  | Secondary page headings, feature names   |
| `text-3xl`  | 28 px  | Page titles (display, with font-display) |

### Heading hierarchy

- **h1** — Page title only. `font-display text-3xl font-semibold leading-tight
  tracking-tight text-fg`. Rendered in Space Grotesk. One per page, always
  inside `<PageHeader>`.
- **h2** — Section heading. `text-xl font-semibold text-fg`. Groups related
  cards or panels. Dialog titles use `text-lg font-semibold leading-none text-fg`.
- **h3** — Card/subsection title. `text-base font-semibold text-fg`.
- **h4** — Field group label. `text-sm font-medium text-fg`.

Do not use `font-display` on anything other than h1.

### Uppercase rule

`uppercase` is allowed ONLY on:

- Table column headers (`<th>`) — via the shared `TableHead` component.
- Standalone definition-term labels (`<dt>`) that label a single key-value pair.
- Single-word dividers (e.g. "OR" between auth methods).

`uppercase` is BANNED on:

- Any heading element (h1–h4).
- Form field labels.
- Section group labels.
- Navigation items.

If in doubt, do not uppercase. The monochrome palette + font-weight alone
provides sufficient hierarchy without case transformation.

### Raw palette ban

Never use Tailwind's built-in color palette directly (e.g. `text-slate-500`,
`bg-red-50`). All colors must go through semantic tokens defined in
`src/style.css` `@theme` block (`text-fg`, `text-fg-muted`, `bg-surface`,
`border-line`, `text-danger`, etc.). ESLint enforces this.

## Code comments

Write no comments by default. Leave a single line in the source only when
**the WHY is non-obvious**: hidden constraints, invariants, a workaround
for a specific bug, behaviour a reader would not expect. Otherwise, none.

- Don't explain WHAT — identifiers should carry that. If deleting the
  comment doesn't hurt comprehension, don't write it.
- Don't write long docstrings or multi-line block comments.
- Don't stamp the current task, caller, PR/MR, or issue number — those
  belong in the commit message and PR description; in source they rot
  during refactors.
- For exported Go symbols that need a doc comment, keep it to a single
  line — don't expand into paragraphs.

## Required checks

Before reporting completion, you must run:

```bash
make check
```

`make check` is the full local gate. It is composed of narrower targets that
CI may run independently based on the changed paths: `make check-go` for sqlc
drift plus non-store Go tests (including all daemon adapters and shared daemon
protocol/gateway packages), `make check-store` for migration/store
integration tests, `make check-web` for web typecheck plus design lint, and
`make check-cli` for CLI/plugin typechecks, and `make check-installer` for
Docker-free installer lifecycle checks, plus `make check-agents-api` for the
execution service. `make check-agents-executor` owns the optional native launcher's
locked unit tests, formatting and Clippy; `make build-agents-executor` independently
builds its release artifact. Native/model fixtures remain explicit acceptance checks.
`make check-agents-harness` adds lightweight exact-patch and packaging checks to
the full gate. Changes to the optional harness artifact also require
`make check-agents-harness-native` (locked native tests, formatting and Clippy),
`make build-agents-harness`, and the applicable actual executor/provider acceptance.
Those expensive native checks run separately and in path-selected CI; a packaging
pass alone is not native runtime acceptance.
Keep the subtargets aligned with
the full gate whenever the required checks change. Daemon-only changes must
trigger the same Go checks in CI as server changes.

In the default full `make check`, `check-agents-api` owns the execution service
and Agents client Go tests after their isolated build. The inherited
`GO_TEST_EXCLUDE` removes those packages only from the default broad Go suite,
so they execute once. Custom `GO_TEST_RUN` or `GO_TEST_ARGS` keeps the original
broad pass, including API tests with those filters or flags. Standalone
`check-go`, `test-go` and `test-fast` retain their full package selection; explicit
`GO_TEST_PACKAGE` overrides retain their existing behavior. Other check commands
and CI target coverage remain unchanged.

Pin the CI vulnerability scanner to a version compatible with the workflow's
Go toolchain; do not use `@latest` for that build-time tool.

- Any DB change must ship with a migration. Migrations are immutable
  the moment they land on `main` — prod has already applied them, so
  editing an existing file only mutates fresh installs. To change
  schema, add a **new** migration numbered strictly above the current
  head; CI (`.github/workflows/migrations.yml`) rejects edits to
  landed files and numeric regressions.
- After editing `server/internal/db/queries/*.sql`, run
  `make sqlc-generate` and commit the regenerated
  `server/internal/db/sqlc/*.go` alongside the SQL. `make check` reruns
  the generator in CI and fails the build on any drift.
- API contracts live on the handler: every `http.HandlerFunc` factory
  must carry a swaggo annotation block (`@Summary`, `@Tags`, `@Param`,
  `@Success/@Failure`, `@Router`) directly above the `func`. After
  changing a handler or its annotations, run `make openapi` to
  regenerate `docs/openapi/openapi.yaml` and commit the diff alongside
  the code. Do NOT edit the YAML by hand — CI regenerates it and fails
  the build on any drift. See `server/internal/api/health.go:livenessHandler`
  and `server/internal/dev/routes.go:listWorkspaceEnabledAgents` for
  the reference style.
- Feishu WebSocket reaction-created and reaction-deleted notifications are
  acknowledged without starting runs or changing send/undo reaction state.
  Keep unsupported-event and malformed-payload errors observable.
- Feishu WebSocket SDK and lifecycle logs must redact connection URL query
  strings before writing them, without changing the URLs used to connect.
  Omit each field's tail after its first `?`, without requiring a valid URL
  prefix; preserve separate SDK correlation fields and structured route context.
- Use `internal/obs/log` for all logging — never `slog.Default()`,
  `log.Println`, `fmt.Println`, or a hand-rolled `*slog.Logger`. The
  linter (`forbidigo`) rejects direct `slog.Default()` outside
  `internal/obs/log` itself. Entry points:
  - `log.Info(ctx, ...)` / `log.Warn(ctx, ...)` / `log.Error(ctx, ...)`
    for request-scoped logs (routes through ContextHandler → trace_id
    attribution).
  - `log.Bg()` for ctx-less startup / shutdown / init code that runs
    outside a request.
  - `log.With("component", "foo")` when you need a scoped `*slog.Logger`
    to hold on to (e.g. inside a handler struct).

## Local CI parity

Before pushing, run these locally so you don't burn a round-trip on
GitHub Actions:

```
make check                    # full required repository gate
make check-go                 # sqlc drift + non-store Go tests
make check-store              # migration + store integration tests
make check-web                # web typecheck + design lint
make check-cli                # CLI/plugin typechecks
make openapi                  # regenerate docs/openapi/openapi.yaml
make sqlc-generate            # regenerate internal/db/sqlc/*.go
cd apps/web && pnpm typecheck # TS type-check web
```

If `make openapi` or `make sqlc-generate` produced a diff, commit it
alongside the source change. CI reruns both generators and fails on
any drift.

**sqlc pinned to v1.29.0.** v1.30+ declares `go >= 1.26` in its
go.mod, which would force `go run` to fetch a newer toolchain than
this repo builds under (go 1.25.13). If you bump sqlc, update
`SQLC_VERSION` in both `Makefile` and `.github/workflows/check.yml` in
the same commit. CI caches a small sqlc binary for `make check-go` and
passes it via the `SQLC` make override; local development defaults to
`go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)`.

## Report language

Verification reports and delivery reports default to English.

Except for user-facing internationalized bilingual copy, comments and
documentation must be written in English.

### Execution token measurements

- Codex preserves input, cached input, output, reasoning output and total token
  counters through the optional daemon `usage.tokens` object. Native thread totals
  are differenced against the restored baseline once per Turn. Incomplete or
  regressing baselines must not become complete measurements; legacy input/output
  fields retain their existing product behavior.
- Agents API atomically projects the latest complete per-Turn measurement alongside
  journal/terminal writes, including applied cancellation receipts. Snapshots replace,
  rather than add to, previous values;
  duplicate usage and Done frames cannot double count. Failure and cancellation
  retain already measured usage even when the terminal payload omits it.
- Session usage sums recorded Turn measurements only, as best-effort usage under
  the pinned protocol. No measurements means null; explicitly measured zero remains
  zero. Old records without the complete breakdown stay unknown. This is neither
  pricing nor a claim that unreported work consumed zero tokens.
