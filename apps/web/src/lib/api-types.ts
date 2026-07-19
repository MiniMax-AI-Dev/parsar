/**
 * Server-side type contracts for Parsar admin API.
 * Source of truth: docs/openapi/openapi.yaml + server/internal/store/store.go.
 */

/* --- Models -------------------------------------------------------------
 *
 * Shared org-global model catalog. Each model picks one credential mode at
 * create time and the choice is immutable:
 *   - inline_secret: bind a shared `secrets.id` (org-wide credential).
 *     Falsy when "pending configuration".
 *   - credential_ref: bind a `credential_kinds.code` (per-user credential
 *     resolved at run-time from `user_credentials`).
 *
 * To change mode / provider_type / adapter, create a new model — PATCH
 * does not touch those fields.
 */

export type ModelCredentialMode = "inline_secret" | "credential_ref"

export interface Model {
  id: string
  slug: string
  name: string
  provider_type: string
  adapter: string
  base_url: string
  model_key: string
  credential_mode: ModelCredentialMode
  /** Set iff credential_mode === "inline_secret". Empty string = pending. */
  secret_id?: string
  /** Set iff credential_mode === "credential_ref". */
  credential_kind_code?: string
  status: "active" | "disabled"
  /** capabilities / limits / headers / modalities / options all live here. */
  config: Record<string, unknown>
  /** User id of the creator. Empty when the row predates the field (seed). */
  created_by?: string
  created_at: string
  updated_at: string
}

export interface ListModelsResponse {
  models: Model[]
}

/**
 * Body for POST /api/v1/workspaces/{wsId}/models.
 * `workspaceID` in the URL is only used for RBAC; the model itself is stored
 * org-globally.
 */
export interface CreateModelRequest {
  name: string
  provider_type: string
  adapter: string
  base_url: string
  model_key: string
  credential_mode: ModelCredentialMode
  /** Required when credential_mode === "inline_secret". */
  secret_id?: string
  /** Required when credential_mode === "credential_ref". */
  credential_kind_code?: string
  capabilities?: Record<string, unknown>
  limits?: Record<string, unknown>
  config?: Record<string, unknown>
}

/**
 * Body for PATCH /api/v1/workspaces/{wsId}/models/{id}.
 * To change credential_mode / provider_type / adapter, create a new model.
 */
export interface UpdateModelRequest {
  name: string
  model_key: string
  base_url?: string
  secret_id?: string
  credential_kind_code?: string
  capabilities?: Record<string, unknown>
  limits?: Record<string, unknown>
  config?: Record<string, unknown>
}

/* --- Agents ------------------------------------------------------------- */

export type AgentRuntime = "local" | "sandbox"

/**
 * Canonical workspace Agent envelope (CreateAgentResponse / DeleteAgentResponse).
 * One row per agent — single `id`, single merged `config`.
 */
export interface AgentSummary {
  id: string
  workspace_id: string
  name: string
  slug: string
  description: string
  connector_type: string
  visibility: "workspace" | "tenant" | "public"
  status: string
  capabilities: string[]
  config: Record<string, unknown>
  created_at: string
  updated_at: string
}

/**
 * Workspace Agent row as returned by the admin list, enriched with derived
 * runtime / sandbox / creator fields. Use `id` for navigation.
 */
export interface Agent {
  id: string
  workspace_id: string
  name: string
  slug: string
  description: string
  connector_type: string
  status: "active" | "disabled" | "error"
  runtime?: AgentRuntime | null
  config: Record<string, unknown>
  /**
   * Workspace-level Agent visibility. Empty / missing → treat as
   * "workspace" (safe default).
   */
  visibility?: "workspace" | "tenant" | "public"
  /**
   * Agent creator. Name is empty when the creating user is deleted or the
   * row pre-dates the field.
   */
  created_by_user_id?: string
  created_by_name?: string
  enabled_at?: string
  /**
   * Explicit runtime binding. When set, the admin list renders "{kind} ·
   * {name}". Empty → "unbound" warning (dispatch is blocked).
   *
   * `runtime_kind` mirrors `runtimes.type`; distinct from the legacy
   * top-level `runtime` field above (pre-v5 placement metadata).
   */
  runtime_id?: string
  runtime_name?: string
  runtime_kind?: string
  runtime_liveness?: string
  /**
   * Currently-bound sandbox for this agent. `sandbox_external_id` is the
   * provider-issued id (e.g. E2B's). Empty when no sandbox is bound.
   *
   * `sandbox_status` mirrors `sandboxes.lifecycle_status`. Same
   * `allocation_status = 'bound' AND killed_at IS NULL` predicate as the
   * detail-page sandbox tab.
   */
  sandbox_external_id?: string
  sandbox_status?: string
}

export interface AgentDetail extends Agent {
  agent_id?: string
  profile?: Record<string, unknown>
  created_at: string
  updated_at: string
}

export type AgentDetailResponse = AgentDetail | { agent: AgentDetail }

export interface ListAgentsResponse {
  agents: Agent[]
}

export interface InitialAgentCapabilityRequest {
  capability_version_id: string
  configuration?: Record<string, unknown>
  /**
   * "latest" follows the capability's current latest version at every
   * dispatch (re-uploads flow through without rewriting the binding).
   * "pinned" locks the binding to capability_version_id. Empty defaults
   * to "pinned" on the server (the safer migration default); the
   * create-agent dialog sends "latest" for new bindings unless the user
   * explicitly picks a specific version.
   */
  pinning_mode?: "latest" | "pinned"
}

/**
 * AgentInlineNewSecret asks the server to materialise a `capability_inline`
 * secret during agent creation, then auto-bind its id into either
 * `config.credential_bindings[kind]` or (when `is_model`) `model_credential_binding`.
 * The plaintext is encrypted server-side and never persisted in cleartext.
 */
export interface AgentInlineNewSecret {
  kind: string
  is_model?: boolean
  display_name?: string
  plaintext: string
}

export interface CreateAgentRequest {
  name: string
  /** Optional explicit stable identifier; omit to let the server allocate a random system slug (`agent-<12hex>`). */
  slug?: string
  description?: string
  connector_type: string
  default_model_id?: string
  system_prompt?: string
  capabilities: string[]
  initial_capabilities?: InitialAgentCapabilityRequest[]
  visibility?: "workspace" | "tenant" | "public"
  runtime?: AgentRuntime
  config?: Record<string, unknown>
  /** New shared secrets to create + bind atomically during agent creation. */
  inline_new_secrets?: AgentInlineNewSecret[]
}

export interface UpdateAgentRequest {
  name?: string
  description?: string
  connector_type?: string
  default_model_id?: string
  system_prompt?: string
  capabilities?: string[]
  runtime?: AgentRuntime | null
  config?: Record<string, unknown>
  /** New shared secrets to create + bind atomically during an edit. */
  inline_new_secrets?: AgentInlineNewSecret[]
}

export interface CreateAgentResponse {
  agent: AgentSummary
  initial_capabilities?: AgentCapability[]
}

export interface DeleteAgentResponse {
  agent: AgentSummary
}

/* --- Capabilities -------------------------------------------------------- */

export type CapabilityType = "skill" | "mcp" | "plugin" | "system_prompt"

export interface RequiredCredential {
  kind: string
  required: boolean
  description?: string
}

export interface Capability {
  id: string
  capability_id?: string
  workspace_id: string
  type: CapabilityType
  name: string
  description: string
  scope?: "private" | "public"
  visibility?: "workspace" | "public"
  status: "active" | "disabled"
  required_credentials?: RequiredCredential[]
  deprecated_at?: string | null
  from_marketplace?: boolean
  source_workspace_id?: string
  source_workspace_name?: string
  latest_version_id?: string
  latest_version?: string
  latest_published_version?: string
  latest_version_created_at?: string
  pinned_version_id?: string
  pinned_version?: string
  enabled_agent_count?: number
  creator_id: string
  created_at: string
  updated_at: string
  deleted_at?: string
  /** True for runtime-injected built-ins (no capability_version row). */
  built_in?: boolean
  /** Stable server-name key for built-ins (matches the toggle route param). */
  builtin_key?: string
}

export interface CapabilityVersion {
  id: string
  capability_id: string
  version: string
  git_repo_url?: string
  git_ref?: string
  path?: string
  content?: Record<string, unknown>
  /**
   * The user's raw paste at import time, wrapped as `{ format, body }` — opaque
   * to the UI except when the "add new version" dialog prefills the editor
   * from the latest version's source (see capabilities/index.tsx).
   */
  source_payload?: { format?: string; body?: string } | Record<string, unknown>
  schema_version?: number
  /**
   * Cleaned canonical spec (server/internal/capability/canonical). Present on
   * versions written through the import flow; absent on legacy versions that
   * only carry `content`. View dialogs prefer this when present.
   */
  canonical_spec?: Record<string, unknown>
  required_credentials?: RequiredCredential[]
  /** OSS object key for plugin / skill-zip versions. Empty for mcp / skill-markdown. */
  oss_key?: string
  /** SHA-256 of the OSS blob; pairs with oss_key. */
  sha256?: string
  creator_id: string
  created_at: string
}

export interface AgentCapability {
  id: string
  agent_id: string
  capability_id: string
  workspace_id?: string
  source_workspace_name?: string
  type?: CapabilityType
  name?: string
  description?: string
  visibility?: "workspace" | "public"
  status?: "active" | "disabled"
  required_credentials?: RequiredCredential[]
  deprecated_at?: string | null
  capability_version_id: string
  version?: string
  latest_version_id?: string
  latest_version?: string
  latest_version_created_at?: string
  enabled: boolean
  configuration: Record<string, unknown>
  /** "latest" tracks newest version on every dispatch; "pinned" locks capability_version_id. */
  pinning_mode?: "latest" | "pinned"
  created_at: string
  updated_at: string
  capability?: Capability
  /** True for runtime-injected built-ins (no capability_version row). */
  built_in?: boolean
  /** Stable server-name key for built-ins; used by the toggle mutation. */
  builtin_key?: string
}

export interface EnableAgentCapabilityRequest {
  configuration?: Record<string, unknown>
  /** See AgentCapability.pinning_mode. Empty defaults to "pinned" server-side. */
  pinning_mode?: "latest" | "pinned"
}

export interface ListCapabilitiesResponse {
  workspace_id: string
  capabilities: Capability[]
  // Marketplace capabilities the workspace has already installed (and a
  // local agent may already be bound to). Always present.
  marketplace_installs?: Capability[]
  // Public marketplace capabilities this workspace has NOT installed yet
  // — surfaced in the Agent picker's "capability marketplace" section. Filtered
  // server-side to exclude installed and self-published rows.
  marketplace_available?: MarketplaceCapability[]
  page?: number
  page_size?: number
  total?: number
}

export interface MarketplaceCapability {
  capability_id: string
  source_workspace_name: string
  type: CapabilityType
  name: string
  description: string
  visibility: string
  status: string
  required_credentials?: RequiredCredential[]
  deprecated_at?: string | null
  latest_version_id: string
  latest_version: string
  latest_version_created_at: string
  installed: boolean
  self_published: boolean
}

export type GetCapabilityResponse = Capability

export interface ListCapabilityVersionsResponse {
  capability_id: string
  versions: CapabilityVersion[]
}

export interface ListAgentCapabilitiesResponse {
  workspace_id: string
  agent_id: string
  installed: AgentCapability[]
  available: Capability[]
}

/* --- User Credentials --------------------------------------------------- */

export interface UserCredential {
  id: string
  kind: string
  display_name: string
  last_used_at?: string | null
  created_at: string
  updated_at: string
}

export interface ListMyCredentialsResponse {
  credentials: UserCredential[]
}

export interface UserCredentialCreateRequest {
  kind: string
  display_name?: string
  plaintext_value: string
}

export interface UserCredentialPatchRequest {
  display_name?: string
  plaintext_value?: string
}

/* --- Agent Runs --------------------------------------------------------- */

export type AgentRunStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "interrupted"


export interface AgentRunSummary {
  id: string
  workspace_id: string
  conversation_id?: string
  trigger_message_id?: string
  output_message_id?: string
  agent_id?: string
  agent_name?: string
  agent_slug?: string
  connector_type: string
  status: AgentRunStatus
  error_summary?: string
  user_facing_reason?: string
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface ListAgentRunsResponse {
  agent_runs: AgentRunSummary[]
  total: number
  limit: number
  offset: number
  statuses?: string[] | null
}

export interface AgentRunRuntimeSnapshot {
  id?: string
  name?: string
  type?: string
  provider?: string
  connector_type?: string
  agent_kind?: string
  runtime_mode?: string
  execution_place?: string
  governance_mode?: string
  device_id?: string
  sandbox_id?: string
  managed_model_id?: string
  capabilities?: Record<string, boolean>
  liveness?: string
  hostname?: string
  version?: string
  last_heartbeat_at?: string
  working_directory?: string
  captured_at?: string
}

export interface AgentRunOutputMessage {
  id: string
  workspace_id: string
  conversation_id: string
  sender_type: string
  sender_id?: string
  kind: string
  content_format: string
  content: string
  metadata: Record<string, unknown>
  created_at: string
}

export interface AgentRunArtifact {
  id: string
  agent_run_id: string
  name: string
  /**
   * Storage tier — WHERE the bytes live. Replaces the legacy
   * `artifact_type` column (which mixed medium + kind).
   */
  medium: string
  /**
   * What the artifact represents (e.g. `screenshot`, `tool_output`,
   * `attachment`). Independent of medium so the same `kind=screenshot`
   * can live in `medium=blob` or `medium=inline`.
   */
  kind: string
  uri: string
  visibility: string
  metadata: Record<string, unknown>
  created_at: string
}

/**
 * Agent run detail returns a richer envelope with related artifacts /
 * usage rows. Field names mirror the Go shape.
 */
export interface AgentRunDetail extends AgentRunSummary {
  requested_by_type: string
  requested_by_id?: string
  external_run_id?: string
  metadata: Record<string, unknown>
  transcript?: string
  updated_at: string
  runtime?: AgentRunRuntimeSnapshot
  output_message?: AgentRunOutputMessage
  artifacts?: AgentRunArtifact[]
  usage?: UsageLog[]
  events?: AgentRunEvent[]
}

export type AgentRunEventKind =
  | "message.delta"
  | "message.complete"
  | "tool.call"
  | "tool.result"
  | "permission.asked"
  | "permission.replied"
  | "model.changed"
  | "session.error"
  | "run.started"
  | "run.queued"
  | "run.completed"
  | "run.failed"
  | "run.cancelled"
  | "run.requeued"

export interface AgentRunEvent {
  id: string
  sequence: number
  event_kind: AgentRunEventKind
  payload: Record<string, unknown>
  occurred_at: string
  created_at?: string
}

export interface ListAgentRunEventsResponse {
  events: AgentRunEvent[]
}

export interface StartAgentRunResponse {
  run_id: string
  status: "running" | string
}

export type AgentRunStreamEvent =
  | { type: "delta"; delta: string }
  | { type: "done"; final: { content: string } }
  | { type: "error"; error: string }
  | { type: "tool"; tool: StreamToolEvent }

export interface StreamToolEvent {
  id?: string
  name: string
  stage: string
  args?: Record<string, unknown>
  result?: Record<string, unknown>
}


/* --- Audit Records ------------------------------------------------------ */

/**
 * 5-category audit source taxonomy. Matches the `source` enum on
 * `audit_records.source`. Identity & data are reserved for later phases.
 */
export type AuditSource = "identity" | "admin" | "runtime" | "approval" | "data"

export type AuditActorType = "user" | "agent" | "system" | "external"

export interface AuditRecord {
  /** Bigint primary key from `audit_records.id`. Serialized as a JS number. */
  id: number
  occurred_at: string
  source: AuditSource
  event_type: string
  actor_type: AuditActorType
  actor_id?: string
  target_type?: string
  target_id?: string
  workspace_id?: string
  payload?: Record<string, unknown>
}

export interface ListAuditRecordsResponse {
  source?: string
  event_type?: string
  target_type?: string
  audit_records: AuditRecord[]
}

/* --- Usage Logs --------------------------------------------------------- */

export interface UsageLog {
  id: string
  workspace_id: string
  agent_run_id?: string
  provider: string
  model: string
  input_tokens: number
  output_tokens: number
  cost_usd: number
  raw?: Record<string, unknown>
  created_at: string
}

export interface ListUsageLogsResponse {
  agent_run_id?: string
  usage_logs: UsageLog[]
}

/* --- Conversations ------------------------------------------------------ */

/**
 * Channel a conversation lives on. WHERE it originated.
 * Replaces the legacy `type` column (which mixed surface + form).
 */
export type ConversationSurface = "web" | "im" | "api"

/**
 * Conversation shape — HOW participants interact on that surface.
 * `thread` (web), `group`/`dm` (im), `oneshot` (api). Independent
 * of surface so an IM surface can host either a group room or a DM
 * without splitting tables.
 */
export type ConversationForm = "thread" | "group" | "dm" | "oneshot"

export interface Conversation {
  id: string
  workspace_id: string
  surface: ConversationSurface
  form: ConversationForm
  title: string
  status: string
  metadata: Record<string, unknown>
  /**
   * Derived from metadata.primary_agent_id via a JOIN. Empty string when no
   * primary agent is bound or the bound agent has been soft-deleted.
   */
  primary_agent_id?: string
  primary_agent_name?: string
  created_at: string
  updated_at: string
}

export interface ConversationListItem extends Conversation {
  message_count: number
  last_message_at?: string | null
  last_message_preview?: string
  last_message_sender_type?: string
}

export interface ListConversationsResponse {
  conversations: ConversationListItem[]
}

export interface CreateConversationRequest {
  title: string
  /** Origin channel. Defaults to `web` server-side when omitted. */
  surface?: ConversationSurface
  /**
   * Conversation shape. Defaults server-side per surface: `thread` for web,
   * `group` for im, `oneshot` for api.
   */
  form?: ConversationForm
  /**
   * Optional agent id to bind as the conversation's primary agent.
   * Persisted to metadata.primary_agent_id; locked after creation.
   */
  agent_id?: string
  metadata?: Record<string, unknown>
}

/**
 * Timeline returned by GET /api/v1/conversations/{id}/timeline.
 */
export interface ConversationTimelineMessage {
  id: string
  conversation_id: string
  sender_type: string
  sender_id?: string
  /**
   * Message intent / WHAT. Examples: `message`, `runtime_error`,
   * `permission_request`, `tool_call`. Replaces the legacy
   * `message_type` column (which mixed kind + format).
   */
  kind?: string
  /**
   * HOW to render `content`. Separate from `kind` so a `runtime_error`
   * rendered as markdown vs plain text is the same kind, different
   * format.
   */
  content_format?: "text" | "markdown" | "json" | "html"
  content: string
  metadata?: Record<string, unknown>
  runs?: ConversationTimelineRun[]
  created_at: string
}

export interface ConversationTimelineRun {
  id: string
  status: string
  error_summary?: string
  user_facing_reason?: string
  agent_name?: string
  agent_slug?: string
  trigger_message_id?: string
  output_message_id?: string
  connector_type?: string
  steps?: ToolStep[]
  /**
   * 1-indexed position in the per-(conversation, agent) serial queue;
   * populated only for status === "queued". Absent / 0 falls back
   * to a bare "queued" badge.
   */
  queue_position?: number
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface ToolStep {
  tool_call_id: string
  name: string
  // Server emits only "running" | "completed"; "failed" is a frontend-only
  // fallback derived in MessageRow when the enclosing run is failed but a
  // step never received its tool.result event.
  status: "running" | "completed" | "failed"
  args?: Record<string, unknown>
  result?: Record<string, unknown>
  occurred_at: string
}

export interface ConversationTimeline {
  conversation_id: string
  messages: ConversationTimelineMessage[]
  agent_runs: ConversationTimelineRun[]
}

export interface SendUserMessageRequest {
  content: string
}

export interface SendUserMessageResponse {
  message: ConversationTimelineMessage
  agent_run_id?: string | null
  dispatched_agent_count?: number
  run_ids?: string[]
  created_at?: string
}

/* --- Connectors (registry view) ---------------------------------------- */

export interface ConnectorSummary {
  connector_type: string
  label: string
  status: "ready" | "needs_config" | "offline" | "unknown" | string
  agent_count: number
  agent_slugs: string[]
}

export interface ListConnectorsResponse {
  connectors: ConnectorSummary[]
}

/* --- Gateways (built-in registry) -------------------------------------- */

export type GatewayStatus = "active" | "not_configured" | "degraded" | "offline"
export type GatewayPhase = "phase_1" | "phase_2" | "phase_3"

export interface GatewayRegistryEntry {
  type: string
  label: string
  status: GatewayStatus
  phase: GatewayPhase
  description: string
}

export interface ListGatewaysResponse {
  workspace_id?: string
  gateways: GatewayRegistryEntry[]
}

/* --- Secrets ------------------------------------------------------------ */

export type SecretStatus = "active" | "disabled"

/**
 * Org-global secret metadata. The encrypted payload is never returned by
 * the API — only the masked preview and metadata. `slug` is the stable
 * auto-generated machine ID; `name` is the free-form display label.
 */
export interface Secret {
  id: string
  slug: string
  name: string
  kind: string
  provider: string
  auth_type: string
  key_version: string
  status: SecretStatus
  masked: string
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface ListSecretsResponse {
  secrets: Secret[]
}

export interface CreateSecretRequest {
  name: string
  /** Defaults to `model_provider` server-side when omitted. */
  kind?: string
  provider: string
  auth_type: string
  /** Raw payload object — encrypted server-side before storage. */
  payload: Record<string, unknown>
}

/* --- Members ---------------------------------------------------------- */

export type MemberRole = "owner" | "admin" | "member" | "viewer"
export type UserStatus = "active" | "disabled"

/** Workspace-level membership, joined with the user it belongs to. */
export interface WorkspaceMember {
  id: string
  workspace_id: string
  user_id: string
  role: MemberRole
  user_email: string
  user_name: string
  user_status: UserStatus
  created_at: string
  updated_at: string
}

export interface ListWorkspaceMembersResponse {
  workspace_id: string
  members: WorkspaceMember[]
}

/** Body for POST /api/v1/workspaces/{wsId}/members. The user record is
 *  upserted by email. */
export interface AddWorkspaceMemberRequest {
  email: string
  name?: string
  role: MemberRole
}

/** Response from POST .../members: the resulting membership row plus
 *  a `user_created` flag telling the UI whether a fresh user was minted
 *  or an existing one reused. */
export interface AddWorkspaceMemberResponse {
  member: WorkspaceMember
  user_created: boolean
}

/** Response from DELETE .../members/{userId}: the removed membership row. */
export interface RemoveWorkspaceMemberResponse {
  member: WorkspaceMember
}

/* --- Platform user picker --------------------------------------------- */

/** One row from GET /api/v1/users/search — a platform user surfaced by the
 *  "add member" combobox. `avatar_url` is the empty string when the user has
 *  no auth identity yet — render an initial-based placeholder. */
export interface PlatformUser {
  id: string
  email: string
  name: string
  avatar_url: string
  status: string
}

/** Response shape for GET /api/v1/users/search?q=... */
export interface SearchUsersResponse {
  items: PlatformUser[]
}

/* --- Header switcher (workspace picker) -------------------------------- */

/** One workspace the calling user belongs to, joined with the membership role. */
export type WorkspaceVisibility = "public" | "private"

export interface UserWorkspace {
  id: string
  name: string
  slug: string
  visibility: WorkspaceVisibility
  role: MemberRole
  created_at: string
  updated_at: string
}

export interface ListMyWorkspacesResponse {
  user_id: string
  workspaces: UserWorkspace[]
}

/* --- Workspace write requests ----------------------------------------- */

export interface CreateWorkspaceRequest {
  name: string
  /** "public" / "private"; omitted → server defaults to "private". */
  visibility?: WorkspaceVisibility
}

export interface CreateWorkspaceResponse {
  workspace: UserWorkspace
  member: WorkspaceMember
}

export interface UpdateWorkspaceRequest {
  /** Slug is system-generated and immutable; name and visibility are mutable. */
  name?: string
  visibility?: WorkspaceVisibility
}

/* --- Self-service workspace join requests ------------------------- */

/**
 * User-initiated workspace join requests. No new table: request/approve/reject
 * are status transitions on workspace_members rows (pending → active / rejected).
 */

/** A public workspace the current user is allowed to request to join. */
export interface DiscoverableWorkspace {
  id: string
  name: string
  slug: string
  visibility: WorkspaceVisibility
  /** Current active member count, providing context in the discovery list. */
  member_count: number
  /**
   * True when the current user has a pending request for this workspace. The
   * frontend uses this to swap the "Join" button for "Requested, awaiting
   * approval" + "Withdraw" buttons.
   */
  has_pending_request: boolean
  created_at: string
  updated_at: string
}

export interface ListDiscoverableWorkspacesResponse {
  user_id: string
  workspaces: DiscoverableWorkspace[]
  /** Total after filtering (feeds the "View all (N)" label + paginator). */
  total: number
  /** Effective limit applied server-side (matching the request, default 50). */
  limit: number
  /** Offset of the current page. */
  offset: number
}

export interface CreateJoinRequestRequest {
  /** Optional, up to 1000 characters. */
  reason?: string
}

/** A pending request as seen by an owner/admin. */
export interface PendingJoinRequest {
  id: string
  workspace_id: string
  user_id: string
  user_email: string
  user_name: string
  request_reason: string
  requested_at: string
}

export interface CreateJoinRequestResponse {
  request: PendingJoinRequest
}

export interface ListPendingJoinRequestsResponse {
  workspace_id: string
  requests: PendingJoinRequest[]
}

/* --- Common UI envelope ------------------------------------------------- */

export interface ApiErrorEnvelope {
  status: number
  code: string
  message: string
  /** True when the failure is "server unreachable" (network/CORS/proxy/down). */
  unreachable?: boolean
}
