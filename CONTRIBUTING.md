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
  qualifies for low-impact changes under the review policy below.
- Run `make check` (and any relevant E2E target) before requesting review.
- Place worktrees under `.worktrees/<feature-name>/` so they don't litter
  the repo root.

```bash
git fetch origin main
git worktree add .worktrees/feature-name -b feature/name origin/main
```

Direct development on `main` is not allowed. Every session honours this rule.

## Independent blind review

Fix one issue per PR. State the expected behavior, acceptance criteria, and
explicit scope exclusions before implementation. Keep unrelated refactors,
features, formatting, and dependency updates in separate PRs.

Small, low-impact changes may use developer self-review and relevant
verification without a subagent. Examples include isolated copy, spacing,
documentation, and local corrections to an already-reviewed change.

Substantial changes, shared interaction behavior, security-sensitive changes,
and changes whose impact is uncertain require an independent blind review.
After implementation and required checks, ask one fresh subagent to review
the entire diff. Give it requirements, acceptance criteria, project rules,
and scope boundaries, without the developer's conversation, implementation
summary, suspected defects, or previous review findings.

Address blocking findings within the same scope and rerun the relevant checks.
Revisions with material behavior changes need a fresh blind reviewer; small
local corrections may be self-reviewed. Required checks still apply to every
PR. Merge only after checks pass and all blocking findings are resolved.
Record the review method, verification, and PR outcome in the linked issue.

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
- **Connector MVP**: Agent Daemon Connector (`connector_type=agent_daemon`,
  adapter determined by `project_agents.config.agent_kind` — `opencode`,
  `claude_code`, …) plus the HTTP Agent Connector.
- Agent Daemon runs are dispatched to the `parsar-daemon` runtime bound to
  the agent (`project_agents.runtime_id`); the daemon's internal adapter
  picks which CLI actually executes.

## Architecture boundaries

The repo has several concepts that sound similar but must stay separate.
When adding or changing code, name the boundary explicitly in the PR
description and keep ownership on the side listed here.

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

- The root `docker-compose.yml` must be directly runnable with
  `docker compose up -d`. Do not require `install.sh` to pre-generate `.env`
  values for mandatory services to boot. If a service needs a local-only
  shared secret, the compose file must provide a clearly documented dev-only
  default and allow production/Dokploy installs to override it with a stable
  random value.
- The one-command installer is both install and upgrade path. Default GHCR
  images must be pulled before `docker compose up` so `:latest` does not
  silently reuse a stale local image after `main` changes.
- After pulling, the installer prepares its server data mount for the image's
  actual UID/GID and verifies writability before starting services. Only the
  preparation container runs as root; the server retains its configured user.
- Default Compose keeps Claude Code configuration and native session files in
  `/root/.parsar/claude-code`, on the runtime's existing persistent home volume.
  The first installer upgrade stops the old runtime, backs up its legacy
  `~/.claude/` and `~/.claude.json` under the install directory, and migrates
  them before container replacement. Conflicting history aborts the upgrade.
  Do not remove the old container or its volumes before this migration.
  Direct Compose/Dokploy users must run `./install.sh migrate-runtime-history`
  followed by their existing Compose global options (such as `-p`, `-f`, and
  `--env-file`) once before upgrading. This migration-only command does not
  rewrite `.env`, change data mounts/secrets, pull images, or start services.
  Then use the original Compose upgrade command. Wait for active runs to finish
  before upgrading; the runtime is stopped during migration.
  Backups contain private session/configuration data; retain them securely until
  resume is verified. Already-deleted native histories cannot be reconstructed.
- `install.sh` may still write stable random overrides such as
  `PARSAR_MASTER_KEY` and `PARSAR_SHARED_RUNTIME_TOKEN` for safer local
  installs, but raw Compose/Dokploy deployments must not depend on those
  installer-only side effects.
- Keep `install.sh` a thin Compose wrapper. Its CLI is limited to installation
  location, web bind/port, image overrides, and validation. Uncommon deployment
  settings belong in the Compose environment rather than new installer flags.
  The one-time history migration subcommand passes existing Compose global
  options through unchanged so raw deployments retain their configuration.
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
  `--image parsar:local` / `--sandbox-image parsar-sandbox:local`; do not make
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

### Runtime and execution concepts

- The daemon resolves loopback Postgres capability-download URLs through its
  paired server address before calling an adapter. Preserve the signed query
  and resource path; external storage URLs and browser upload URLs are unchanged.
- `connector_type` chooses the protocol Parsar uses to run an agent
  (`agent_daemon`, `http_agent`, ...). It does not say where the process
  runs.
- `runtime_id` chooses the concrete paired runtime/device/sandbox that will
  receive a run. It is a routing handle, not agent configuration.
- `agent_kind` chooses the daemon-side engine (`claude_code`, `codex`,
  `pi`, `opencode`). It is interpreted only by `parsar-daemon`.
- Placement labels such as local device, cloud sandbox, and external agent
  are UI/product concepts. Do not branch business logic on display copy.
  Derive placement from typed runtime/provider/config fields in one shared
  helper per layer.

### Server versus daemon ownership

- Agent exposure through MCP lives in `server/internal/api/agentmcp` and uses
  the standard conversation dispatcher. Personal MCP credentials are stored
  only as hashes, scoped to one user and Agent, expire after 30 days, and are
  checked against current membership and resource state on every request.
  They never authenticate Web sessions or runtime/device APIs. Replacing or
  revoking a credential affects only that user's connection to that Agent.
- MCP calls persist their `mcp` source and real requesting user. The MCP
  endpoint can start a task for its bound Agent and read only that user's runs
  for that Agent; it does not bypass existing approvals or expose runtime
  configuration, raw events, or other users' results. Long runs are retrieved
  with bounded polling; their durable state stays in Parsar, not the MCP session.

- Agent capability reads retain the stored binding version and expose its
  pinning mode. Displayed current versions match the daemon's resolver:
  Skill, Plugin, Bundle, and Knowledge can follow latest metadata (including
  deprecation cutoffs); MCP and System Prompt currently use the stored binding
  version. Config offers automatic-follow choices only for supported types and
  retains per-binding configuration when changing version policy.

- Cross-workspace installed capabilities remain visible and removable after
  unpublishing. Their installation metadata reports source visibility and only
  bound versions while private; marketplace discovery and new installs still
  require a published source.
- Conversation user-message limits count Unicode code points after trimming
  surrounding whitespace, not UTF-8 bytes. Keep route and store validation aligned.
- Agent capability upgrades accept private capabilities from the Agent's own
  workspace; cross-workspace upgrades still require a public, available source.
- The server owns auth, workspaces, agent records, runtime bindings, run
  records, audit/usage persistence, and upstream engine session ids.
- Soft-deleting an Agent preserves workspace-authorized conversation and run
  history, including its identity. History reads expose deletion state; they
  must not allow new messages or retries to execute the deleted Agent.
- Successful explicit Agent capability enable, upgrade, removal, and built-in
  toggle requests emit Agent-targeted audit events with the authenticated actor
  and capability identifiers. Never include configuration or credential values.
- Explicit Agent enable/disable requests pass the requesting user to the store
  for audit attribution. Keep the target Agent separate from the actor, and
  preserve the previous and next status in the event payload.
- `parsar-daemon` owns CLI discovery, process spawning, CLI-specific env,
  cwd selection inside its host/container, permission prompts, and translating
  CLI streams into Parsar daemon protocol frames.
- `internal/agentdaemon/proto` is the only shared wire contract between the
  server and daemon. The daemon must not import `server/internal/...`, and the
  server must not import daemon-internal adapter packages.
- The conversation SSE first-event timer observes run state; it is not an
  execution deadline. Keep waiting while the stored run is queued or running.
  Dispatch retains ownership of execution timeouts and terminal state.
- Any state needed to recover a conversation after a server restart, daemon
  reconnect, or child-process exit must be stored durably by the server.
  In-memory maps may cache waiters or sockets only; they must not be the
  source of truth for conversation/session continuity.
- Work directory validation is a cross-boundary security rule: user input is
  accepted only as an absolute path or `~/...`; daemon-side fallbacks must stay
  under `~/.parsar/`.

### External HTTP Agents

- `connector_type=http` runs use the standard conversation dispatcher and its
  30-minute execution deadline. The HTTP connector performs one JSON POST and
  emits one final reply; the dispatcher alone persists completion and usage.
  Default development startup uses this same dispatcher. The legacy standalone
  HTTP worker is not supported alongside the server; it bypasses credential
  resolution, serial dispatch, and request cancellation ownership.
- Store `config.http.endpoint` and optional `config.http.secret_id`. Accept the
  historical flat keys on input, but never forward endpoint or credential
  configuration in the request body. Only `agent_config.system_prompt` is sent.
- Bearer secrets use `kind=provider=http_agent`, `auth_type=bearer`, and a
  `{"token": "..."}` encrypted payload. Check active status and management
  workspace on both configuration and every invocation. Global model secrets
  are not HTTP Agent credentials. Reject URL userinfo and all redirects.
- The service owns models, tools, permissions, and conversation history, keyed
  by `conversation_id`. Parsar capability/runtime bindings are not injected.
  Text requests carry the existing `httprunner.AgentRequest` identity fields;
  responses contain nonempty `content` and optional `store.UsageInput` `usage`.
  Responses are limited to 4 MiB. Do not infer unreported usage or prices.
- Stop cancels the outbound request on the executing server instance; this
  initial connector targets single-instance deployments. Cross-instance HTTP
  request cancellation is not supported. Stop does not guarantee termination of work
  inside a remote service; services should honor HTTP request cancellation and
  deduplicate work by `run_id`. Retrying creates a new run.

### Agent editing

- The Agent edit form updates profile, model, and execution settings only.
  Manage existing capability bindings, versions, and capability credentials
  through the Config capability controls. Creation can choose initial bindings.
  Profile edits must omit capability reconciliation and capability credential
  snapshots.

### Agent CLI adapter contract

- Daemon-managed Codex sessions use `approvalPolicy=never` and
  `sandbox=danger-full-access` on both `thread/start` and `thread/resume`,
  including conversations created under an older policy. The daemon owns
  these defaults; no `PARSAR_CODEX_*` approval environment switch is required.
  Explicit engine approval requests still use the durable interaction lifecycle;
  user-input requests continue to wait for a human answer.

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

### Sandbox and local runtime lifecycle

- The default local install path provides one ready-to-use sandbox runtime.
  Do not require the Parsar server container to create sibling Docker
  containers through `/var/run/docker.sock` for normal first-run operation.
- Local Docker lifecycle, cloud sandbox lifecycle, and user-paired devices are
  different providers behind the same daemon protocol. Keep provider-specific
  create/renew/kill logic in `server/internal/sandbox/...` or a narrowly named
  runtime provider; do not spread Docker/E2B calls through handlers,
  connectors, or frontend components.
- Dynamic local sandbox scaling is not a product guarantee. If it is added,
  it must be owned by an explicit local supervisor/runtime provider with a
  reviewed Docker socket boundary, not by ad hoc server-side `docker run`
  calls.
- Eager acquisition must be best-effort. Failure to prewarm a sandbox should
  surface as runtime health/provisioning state, not crash unrelated startup
  paths.
- Cloud sandbox maintenance runs once at server startup and every five minutes.
  Automatic renewal requires a TTL longer than that interval; interrupted
  renewals remain retryable, while a provider rejection disables the policy.
- A `spawning` sandbox binding holds that agent's only reservation slot
  (`uk_sandboxes_active_per_agent` is partial on `killed_at is null`), and the
  loser path waits on `spawning` indefinitely. Any code that reserves a slot
  must therefore guarantee a terminal transition, and an acquire that finds a
  reservation older than the cold-start bound must be able to reclaim it —
  otherwise one crashed cold start wedges the agent permanently.

### Sandbox images

- `infra/sandbox/Dockerfile` (local Docker + generic) and
  `infra/sandbox/e2b.Dockerfile` (e2b.app) must keep their shared runtime
  payload, CLI versions, and hooks aligned; provider-specific bootstrap and
  build mechanics may differ.
  Agent CLI installs live only in `infra/sandbox/scripts/install-agents.sh`,
  which both images run; do not inline per-CLI `npm install -g` / download
  steps in either Dockerfile. That script owns the version pins and the Node
  force-relink that keeps a base image's bundled Node from shadowing ours.
- The image must ship the hook scripts at the absolute paths
  `server/internal/connector/agentdaemon/sandbox_seed.go` seeds into
  `settings.json` (`/opt/parsar/hooks/claude/...`). The hooks fail open, so a
  missing script degrades spec/memory injection silently instead of erroring —
  changing one side means changing the other.
- e2b's template builder is not BuildKit. It rejects multi-stage builds (hence
  the prebuilt binaries in `infra/sandbox/.build/`, staged by
  `make e2b-template`), it does not persist `/tmp` between layers, and it
  lowers `ARG FOO="bar"` keeping the quotes as literal characters — so version
  ARGs in `e2b.Dockerfile` must stay unquoted.
- Build templates with `make e2b-template`. It writes only into
  `infra/sandbox/.build/` (gitignored), never the repo root.

### Testing cloud isolation locally

- The daemon runs inside the cloud sandbox and dials back to
  `PARSAR_PUBLIC_URL`, so a loopback URL cannot work: the sandbox resolves
  `127.0.0.1` to itself and pairing times out. Expose the dev server through a
  tunnel and set `PARSAR_PUBLIC_URL` to that hostname.
- `AGENT_DAEMON_SANDBOX_TEMPLATE` + `PARSAR_E2B_API_KEY` are the two required
  values; see `.env.example` for the full set and their defaults.

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

The Agent form checks workspace runtime status before creating or switching to
cloud execution. Unknown or unavailable status blocks advancing and submitting;
ordinary edits to an existing cloud Agent and local execution stay independent.
Cloud setup opens Runtime's Instances tab separately so form input is retained.
Missing-model setup follows the same pattern: open Models separately and refresh
the catalog from the still-open Agent form. Keep its draft in the mounted form;
do not serialize it into prerequisite URLs or add a second draft lifecycle.

New and cloned Agent forms default invocation scope to workspace, matching the
server default. Scope choices explain the existing Feishu gate without implying
anonymous Web/API access. Public creation clears personal model credential choices
and requires a shared binding for credential-reference models; existing Agents
and their edit forms keep their stored scope.

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
drift plus non-store Go tests, `make check-store` for migration/store
integration tests, `make check-web` for web typecheck plus design lint, and
`make check-cli` for CLI/plugin typechecks, and `make check-installer` for
Docker-free installer lifecycle checks. Keep the subtargets aligned with
the full gate whenever the required checks change.

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
