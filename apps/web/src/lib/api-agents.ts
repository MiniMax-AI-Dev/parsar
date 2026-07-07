import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"
import type {
  Agent,
  AgentRunEvent,
  AgentRunDetail,
  AgentRunStatus,
  AgentSummary,
  CreateAgentRequest,
  CreateAgentResponse,
  DeleteAgentResponse,
  ListAgentRunEventsResponse,
  ListAgentRunsResponse,
  ListAgentsResponse,
  UpdateAgentRequest,
} from "./api-types"

/* --- Query keys --------------------------------------------------------- */

const KEY_AGENTS = (workspaceID: string) => ["admin", "agents", workspaceID] as const
// Statuses are sorted + joined to a stable string (nil/empty → "_all") so the
// "in progress" union tab and pagination produce distinct cache entries.
const KEY_RUNS = (
  workspaceID: string,
  statuses?: AgentRunStatus[] | null,
  offset: number = 0,
  limit: number = 100,
) => {
  const statusKey = statuses && statuses.length > 0 ? [...statuses].sort().join(",") : "_all"
  return ["admin", "agentRuns", workspaceID, statusKey, offset, limit] as const
}
const KEY_RUN = (runID: string) => ["admin", "agentRun", runID] as const
const KEY_RUN_EVENTS = (workspaceID: string, runID: string) => ["admin", "agentRunEvents", workspaceID, runID] as const
const KEY_FEISHU_DIAGNOSTICS = (agentID: string) => ["admin", "agentFeishuDiagnostics", agentID] as const
// Days varies (7/30/90) so it is part of the key — sharing a cache entry across
// windows would flash stale numbers on toggle.
const KEY_AGENT_METRICS = (
  workspaceID: string,
  agentID: string,
  days: number,
) => ["admin", "agentMetrics", workspaceID, agentID, days] as const

/* --- Network ------------------------------------------------------------ */

// The agent-list endpoint keys rows `agent_id` (store.AgentRead) but the app
// reads `.id`; backfill it once here, the choke point feeding useAgents.
function normalizeAgentId(a: Agent): Agent {
  if (a.id) return a
  const agentID = (a as Agent & { agent_id?: string }).agent_id
  return agentID ? { ...a, id: agentID } : a
}

async function listAgents(workspaceID: string | null): Promise<ListAgentsResponse> {
  if (!workspaceID) return { agents: [] }
  const res = await apiRequest<ListAgentsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents`
  )
  return { ...res, agents: (res.agents ?? []).map(normalizeAgentId) }
}

async function listAgentRuns(
  workspaceID: string | null,
  statuses?: AgentRunStatus[] | null,
  offset?: number,
  limit?: number,
): Promise<ListAgentRunsResponse> {
  if (!workspaceID) {
    return { agent_runs: [], total: 0, limit: limit ?? 100, offset: offset ?? 0 }
  }
  // Handler accepts comma-separated `status=a,b` for the union "in progress" tab.
  // Omit the param entirely when no filter so the backend's empty-set branch fires.
  const query: Record<string, string | number | boolean | undefined> = {
    limit: limit ?? 100,
    offset: offset ?? 0,
  }
  if (statuses && statuses.length > 0) {
    query.status = statuses.join(",")
  }
  return apiRequest<ListAgentRunsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agent-runs`,
    { query }
  )
}

async function getRunDetail(
  runID: string
): Promise<AgentRunDetail> {
  return apiRequest<AgentRunDetail>(`/api/v1/agent-runs/${encodeURIComponent(runID)}`)
}

async function listAgentRunEvents(
  workspaceID: string | null,
  runID: string | null,
  afterSequence?: number
): Promise<ListAgentRunEventsResponse> {
  if (!workspaceID || !runID) return { events: [] }
  return apiRequest<ListAgentRunEventsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agent-runs/${encodeURIComponent(runID)}/events`,
    { query: { after_sequence: afterSequence } }
  )
}

async function requeueRunRequest(runID: string, reason?: string) {
  return apiRequest<unknown>(
    `/api/v1/agent-runs/${encodeURIComponent(runID)}/requeue`,
    { method: "POST", body: reason ? { reason } : {} }
  )
}

async function cancelRunRequest(runID: string, reason?: string) {
  return apiRequest<unknown>(
    `/api/v1/agent-runs/${encodeURIComponent(runID)}/cancel`,
    { method: "POST", body: reason ? { reason } : {} }
  )
}

// Bulk cancel every queued / running run in the conversation, regardless of
// agent. Used by "cancel all" and the Feishu /cancel all command.
async function cancelConversationAllRequest(conversationID: string, reason?: string) {
  return apiRequest<unknown>(
    `/api/v1/conversations/${encodeURIComponent(conversationID)}/cancel-all`,
    { method: "POST", body: reason ? { reason } : {} }
  )
}

function noWorkspaceError(): ApiError {
  return new ApiError({
    status: 0,
    code: "no_workspace",
    message: "no workspace selected — pick a workspace first",
    unreachable: false,
  })
}

async function createAgentRequest(
  workspaceID: string,
  body: CreateAgentRequest
): Promise<AgentSummary> {
  const res = await apiRequest<CreateAgentResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents`,
    { method: "POST", body }
  )
  return res.agent
}

async function updateAgentRequest(
  agentID: string,
  body: UpdateAgentRequest
): Promise<AgentSummary> {
  return apiRequest<AgentSummary>(
    `/api/v1/agents/${encodeURIComponent(agentID)}`,
    { method: "PATCH", body }
  )
}

/**
 * AgentVisibility encodes "who can invoke this Agent". See
 * docs/feishu-routing.md §3. `workspace` is the safe default.
 */
export type AgentVisibility = "workspace" | "tenant" | "public"

export interface AgentVisibilityChange {
  agent_id: string
  workspace_id: string
  name: string
  slug: string
  old_visibility: AgentVisibility
  new_visibility: AgentVisibility
  noop?: boolean
}

interface UpdateAgentVisibilityResponse {
  visibility: AgentVisibilityChange
}

async function updateAgentVisibilityRequest(
  agentID: string,
  visibility: AgentVisibility
): Promise<AgentVisibilityChange> {
  const res = await apiRequest<UpdateAgentVisibilityResponse>(
    `/api/v1/agents/${encodeURIComponent(agentID)}/visibility`,
    { method: "PATCH", body: { visibility } }
  )
  return res.visibility
}

async function deleteAgentRequest(agentID: string): Promise<DeleteAgentResponse> {
  return apiRequest<DeleteAgentResponse>(
    `/api/v1/agents/${encodeURIComponent(agentID)}`,
    { method: "DELETE" }
  )
}

async function setAgentStatus(
  agentID: string,
  enabled: boolean
): Promise<AgentSummary> {
  return apiRequest<AgentSummary>(
    `/api/v1/agents/${encodeURIComponent(agentID)}/${enabled ? "enable" : "disable"}`,
    { method: "POST" }
  )
}

export interface UpdateAgentProfileRequest {
  model_id?: string
  workdir?: string
  system_prompt?: string
  config?: Record<string, unknown>
}

async function updateAgentProfileRequest(
  agentID: string,
  body: UpdateAgentProfileRequest
): Promise<unknown> {
  return apiRequest<unknown>(
    `/api/v1/agents/${encodeURIComponent(agentID)}/profile`,
    { method: "POST", body }
  )
}

/* --- React Query hooks -------------------------------------------------- */

export function useAgents(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_AGENTS(workspaceID ?? "_none"),
    queryFn: () => listAgents(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export interface UseAgentRunsOptions {
  // admin "in progress" tab passes ["running","queued"]; null/undefined/empty = no filter.
  statuses?: AgentRunStatus[] | null
  offset?: number
  limit?: number
}

export function useAgentRuns(
  workspaceID: string | null,
  options: UseAgentRunsOptions = {},
) {
  const { statuses, offset = 0, limit = 100 } = options
  return useQuery({
    queryKey: KEY_RUNS(workspaceID ?? "_none", statuses, offset, limit),
    queryFn: () => listAgentRuns(workspaceID, statuses, offset, limit),
    retry: noUnreachableRetry,
    staleTime: 15_000,
    // Keep the previous page on screen while the next one fetches.
    placeholderData: (prev) => prev,
  })
}

// AgentMetrics — backend-aggregated run-history snapshot. success_rate is in
// [0,1]; avg_duration_ms is 0 when no completed run has both started_at and
// finished_at in the window.
export interface AgentMetrics {
  window_days: number
  completed_count: number
  failed_count: number
  success_rate: number
  avg_duration_ms: number
}

async function getAgentMetrics(
  workspaceID: string,
  agentID: string,
  days: number,
): Promise<AgentMetrics> {
  return apiRequest<AgentMetrics>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/metrics`,
    { query: { days } },
  )
}

export function useAgentMetrics(
  workspaceID: string | null,
  agentID: string | null,
  days: number = 30,
) {
  return useQuery({
    queryKey: KEY_AGENT_METRICS(workspaceID ?? "_none", agentID ?? "_none", days),
    queryFn: () => {
      if (!workspaceID || !agentID) {
        throw new Error("workspaceID and agentID are required")
      }
      return getAgentMetrics(workspaceID, agentID, days)
    },
    enabled: !!workspaceID && !!agentID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useAgentRun(runID: string | null, _workspaceIDForMock?: string | null) {
  return useQuery({
    queryKey: KEY_RUN(runID ?? "_none"),
    queryFn: () => {
      if (!runID) throw new Error("runID is required")
      return getRunDetail(runID)
    },
    enabled: !!runID,
    retry: noUnreachableRetry,
    staleTime: 15_000,
  })
}

export function useAgentRunEvents(
  runID: string | null,
  workspaceID: string | null,
  options?: { status?: AgentRunStatus; initialEvents?: AgentRunEvent[] }
) {
  // Running and queued runs can still emit new events, so keep polling.
  const live = options?.status === "running" || options?.status === "queued"
  return useQuery({
    queryKey: KEY_RUN_EVENTS(workspaceID ?? "_none", runID ?? "_none"),
    queryFn: () => listAgentRunEvents(workspaceID, runID),
    enabled: !!runID && !!workspaceID,
    retry: noUnreachableRetry,
    staleTime: live ? 0 : 15_000,
    refetchInterval: live ? 5_000 : false,
    initialData: options?.initialEvents ? { events: options.initialEvents } : undefined,
  })
}

export function useRequeueRun(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ runID, reason }: { runID: string; reason?: string }) => {
      if (!workspaceID) throw noWorkspaceError()
      return requeueRunRequest(runID, reason)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "agentRuns"] })
      void qc.invalidateQueries({ queryKey: ["admin", "agentRun"] })
      void qc.invalidateQueries({ queryKey: ["admin", "agentRunEvents"] })
    },
  })
}

export function useCancelRun(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ runID, reason }: { runID: string; reason?: string }) => {
      if (!workspaceID) throw noWorkspaceError()
      return cancelRunRequest(runID, reason)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "agentRuns"] })
      void qc.invalidateQueries({ queryKey: ["admin", "agentRun"] })
      void qc.invalidateQueries({ queryKey: ["admin", "agentRunEvents"] })
      void qc.invalidateQueries({ queryKey: ["conversation"] })
    },
  })
}

// useCancelConversation drives "cancel all". onSuccess MUST invalidate the
// conversationTimeline query keyed by this conversation — otherwise
// ChatStream's `runs.some(...)` keeps `someRunActive` true and the button +
// "thinking" spinner stay on screen.
export function useCancelConversation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ conversationID, reason }: { conversationID: string; reason?: string }) => {
      return cancelConversationAllRequest(conversationID, reason)
    },
    onSuccess: (_, { conversationID }) => {
      void qc.invalidateQueries({ queryKey: ["admin", "agentRuns"] })
      void qc.invalidateQueries({ queryKey: ["conversation"] })
      void qc.invalidateQueries({ queryKey: ["admin", "conversationTimeline", conversationID] })
    },
  })
}

export function useCreateAgent(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: CreateAgentRequest) => {
      if (!workspaceID) throw noWorkspaceError()
      return createAgentRequest(workspaceID, body)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
    },
  })
}

export function useUpdateAgent(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      agentID,
      body,
    }: {
      agentID: string
      body: UpdateAgentRequest
    }) => {
      return updateAgentRequest(agentID, body)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
    },
  })
}

/**
 * useUpdateAgentVisibility wraps the visibility PATCH endpoint. Callers
 * should drive a confirm-dialog when switching from `public` to a stricter
 * tier — this hook does not enforce that.
 */
export function useUpdateAgentVisibility(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      agentID,
      visibility,
    }: {
      agentID: string
      visibility: AgentVisibility
    }) => {
      return updateAgentVisibilityRequest(agentID, visibility)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
    },
  })
}

/**
 * FeishuConnectorConfig mirrors agents.config.connectors.feishu. *_ref values
 * point at workspace Secret vault entries — never store plain Bot credentials
 * in this struct.
 */
export interface FeishuConnectorConfig {
  enabled: boolean
  app_id: string
  app_secret_ref: string
  verification_token_ref: string
  encrypt_key_ref: string
  bot_open_id: string
  event_mode: "webhook" | "websocket"
  routing_mode: "direct" | "shared"
}

export interface AgentFeishuConnectorChange {
  agent_id: string
  workspace_id: string
  name: string
  slug: string
  old: FeishuConnectorConfig
  new: FeishuConnectorConfig
  updated_at: string
  noop?: boolean
}

interface UpdateAgentFeishuConnectorResponse {
  feishu_connector: AgentFeishuConnectorChange
}

export interface FeishuProvisionBeginResult {
  device_code: string
  user_code: string
  verification_uri: string
  verification_uri_complete: string
  expires_in: number
  interval: number
}

export interface FeishuProvisionResponse {
  status: "pending" | "success" | "error"
  begin?: FeishuProvisionBeginResult
  next_interval_sec?: number
  error?: string
  description?: string
  app_id?: string
  app_secret_ref?: string
  bot_open_id?: string
  bot_name?: string
  feishu_connector?: AgentFeishuConnectorChange
}

export interface FeishuConnectorDiagnostics {
  agent_id: string
  workspace_id: string
  configured: boolean
  enabled: boolean
  event_mode: "webhook" | "websocket"
  app_id_set: boolean
  app_secret_set: boolean
  verification_token_set: boolean
  encrypt_key_set: boolean
  bot_open_id_set: boolean
  conversation_count: number
  inbound_message_count: number
  outbound_message_count: number
  pending_outbound_count: number
  retrying_outbound_count: number
  dead_outbound_count: number
  delivered_outbound_count: number
  last_inbound_at?: string
  last_outbound_at?: string
  last_delivered_at?: string
  last_error?: string
  last_error_at?: string
}

interface FeishuConnectorDiagnosticsResponse {
  diagnostics: FeishuConnectorDiagnostics
}

async function getAgentFeishuConnectorDiagnosticsRequest(
  agentID: string
): Promise<FeishuConnectorDiagnostics> {
  const res = await apiRequest<FeishuConnectorDiagnosticsResponse>(
    `/api/v1/agents/${encodeURIComponent(agentID)}/connector/feishu/diagnostics`
  )
  return res.diagnostics
}

async function updateAgentFeishuConnectorRequest(
  agentID: string,
  body: FeishuConnectorConfig
): Promise<AgentFeishuConnectorChange> {
  const res = await apiRequest<UpdateAgentFeishuConnectorResponse>(
    `/api/v1/agents/${encodeURIComponent(agentID)}/connector/feishu`,
    { method: "PATCH", body }
  )
  return res.feishu_connector
}

async function beginAgentFeishuProvisioningRequest(
  agentID: string
): Promise<FeishuProvisionResponse> {
  return apiRequest<FeishuProvisionResponse>(
    `/api/v1/agents/${encodeURIComponent(agentID)}/connector/feishu/provision/begin`,
    { method: "POST" }
  )
}

async function pollAgentFeishuProvisioningRequest(
  agentID: string,
  body: {
    device_code: string
    interval_sec?: number
    tenant_brand?: string
  }
): Promise<FeishuProvisionResponse> {
  return apiRequest<FeishuProvisionResponse>(
    `/api/v1/agents/${encodeURIComponent(agentID)}/connector/feishu/provision/poll`,
    { method: "POST", body }
  )
}

export function useAgentFeishuConnectorDiagnostics(agentID: string | null) {
  return useQuery({
    queryKey: KEY_FEISHU_DIAGNOSTICS(agentID ?? "_none"),
    queryFn: () => getAgentFeishuConnectorDiagnosticsRequest(agentID ?? ""),
    enabled: Boolean(agentID),
    retry: noUnreachableRetry,
    staleTime: 15_000,
    refetchInterval: 15_000,
  })
}

/**
 * useUpdateAgentFeishuConnector wraps the Feishu Bot binding PATCH endpoint.
 * Surface `code` from the ApiError envelope for field-specific errors:
 *   - 422 `feishu_connector_incomplete` — enabled=true with empty required field
 *   - 409 `feishu_app_id_in_use` — app_id collides with another active+enabled Agent
 */
export function useUpdateAgentFeishuConnector(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      agentID,
      config,
    }: {
      agentID: string
      config: FeishuConnectorConfig
    }) => updateAgentFeishuConnectorRequest(agentID, config),
    onSuccess: (_change, variables) => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
      void qc.invalidateQueries({ queryKey: KEY_FEISHU_DIAGNOSTICS(variables.agentID) })
    },
  })
}

export function useBeginAgentFeishuProvisioning(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (agentID: string) => beginAgentFeishuProvisioningRequest(agentID),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
    },
  })
}

export function usePollAgentFeishuProvisioning(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      agentID,
      deviceCode,
      intervalSec,
      tenantBrand,
    }: {
      agentID: string
      deviceCode: string
      intervalSec?: number
      tenantBrand?: string
    }) => pollAgentFeishuProvisioningRequest(agentID, {
      device_code: deviceCode,
      interval_sec: intervalSec,
      tenant_brand: tenantBrand,
    }),
    onSuccess: (res, variables) => {
      if (res.status === "success") {
        void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
        void qc.invalidateQueries({ queryKey: KEY_FEISHU_DIAGNOSTICS(variables.agentID) })
        void qc.invalidateQueries({ queryKey: ["admin", "secrets"] })
      }
    },
  })
}

export function useSetAgentStatus(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      agentID,
      enabled,
    }: {
      agentID: string
      enabled: boolean
    }) => setAgentStatus(agentID, enabled),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
    },
  })
}

export function useUpdateAgentProfile(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      agentID,
      body,
    }: {
      agentID: string
      body: UpdateAgentProfileRequest
    }) => updateAgentProfileRequest(agentID, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
    },
  })
}

export function useDeleteAgent(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (agentID: string) => deleteAgentRequest(agentID),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_AGENTS(workspaceID ?? "_none") })
    },
  })
}

export type { Agent }
