import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"
import type {
  AgentCapability,
  Capability,
  CapabilityType,
  CapabilityVersion,
  GetCapabilityResponse,
  EnableAgentCapabilityRequest,
  ListCapabilitiesResponse,
  ListCapabilityVersionsResponse,
  ListAgentCapabilitiesResponse,
  RequiredCredential,
} from "./api-types"
import type { CanonicalSpec, SystemPromptMode } from "../pages/admin/capabilities/types"

export const KEY_CAPABILITIES = (workspaceID: string, name = "", type = "", page?: number, pageSize?: number) =>
  ["admin", "capabilities", workspaceID, name, type, page ?? "_all", pageSize ?? "_all"] as const
// Workspace-prefix key needed for invalidation — tanstack-query matches by
// prefix and the full KEY_CAPABILITIES is 5 elements deep.
export const KEY_CAPABILITIES_WORKSPACE = (workspaceID: string) =>
  ["admin", "capabilities", workspaceID] as const
const KEY_CAPABILITY = (workspaceID: string, capabilityID: string) =>
  ["admin", "capability", workspaceID, capabilityID] as const
export const KEY_CAPABILITY_VERSIONS = (workspaceID: string, capabilityID: string) =>
  ["admin", "capabilityVersions", workspaceID, capabilityID] as const
export const KEY_AGENT_CAPABILITIES = (workspaceID: string, agentID: string) =>
  ["admin", "agentCapabilities", workspaceID, agentID] as const

export interface CreateCapabilityRequest {
  type: CapabilityType
  name: string
  description?: string
  scope?: "private"
  status?: "active"
  required_credentials?: RequiredCredential[]
  version?: string
  git_repo_url?: string
  git_ref?: string
  path?: string
  content?: Record<string, unknown>
  schema_version?: number
  canonical_spec?: CanonicalSpec
}

export interface UpdateCapabilityRequest {
  name?: string
  description?: string
  status?: "active" | "disabled"
}

export interface CreateCapabilityVersionRequest {
  version: string
  git_repo_url?: string
  git_ref?: string
  path?: string
  content?: Record<string, unknown>
  required_credentials?: RequiredCredential[]
  schema_version?: number
  canonical_spec?: CanonicalSpec
}

export function systemPromptCapabilityPayload(input: {
  name: string
  description: string
  version: string
  prompt: string
  mode: SystemPromptMode
}): CreateCapabilityRequest {
  return {
    type: "system_prompt",
    name: input.name,
    description: input.description,
    scope: "private",
    status: "active",
    version: input.version,
    schema_version: 1,
    canonical_spec: {
      schema_version: 1,
      kind: "system_prompt",
      system_prompt: { prompt: input.prompt, mode: input.mode },
    },
  }
}

export function agentCapabilityVersionID(item: AgentCapability): string {
  return item.capability_version_id
}

export function skillVersionRef(version: CapabilityVersion): string {
  return version.git_ref ?? ""
}

export function skillCapabilityPayload(input: {
  name: string
  description: string
  requiredCredentials: RequiredCredential[]
  version: string
  repoURL: string
  ref: string
  path: string
}): CreateCapabilityRequest {
  return {
    type: "skill",
    name: input.name,
    description: input.description,
    scope: "private",
    status: "active",
    required_credentials: input.requiredCredentials,
    version: input.version,
    git_repo_url: input.repoURL,
    git_ref: input.ref,
    path: input.path,
  }
}

async function listCapabilities(
  workspaceID: string | null,
  name = "",
  type = "",
  page?: number,
  pageSize?: number
): Promise<ListCapabilitiesResponse> {
  if (!workspaceID) return { workspace_id: "", capabilities: [] }
  const query: Record<string, string> = {}
  const trimmedName = name.trim()
  const trimmedType = type.trim()
  if (trimmedName) query.name = trimmedName
  if (trimmedType) query.type = trimmedType
  if (page !== undefined) query.page = String(page)
  if (pageSize !== undefined) query.page_size = String(pageSize)
  return apiRequest<ListCapabilitiesResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities`,
    Object.keys(query).length ? { query } : undefined
  )
}

async function getCapability(
  workspaceID: string,
  capabilityID: string
): Promise<GetCapabilityResponse> {
  return apiRequest<GetCapabilityResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}`
  )
}

export async function listCapabilityVersions(
  workspaceID: string,
  capabilityID: string
): Promise<ListCapabilityVersionsResponse> {
  return apiRequest<ListCapabilityVersionsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/versions`
  )
}

async function createCapability(workspaceID: string, body: CreateCapabilityRequest): Promise<Capability> {
  return apiRequest<Capability>(`/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities`, {
    method: "POST",
    body,
  })
}

async function updateCapability(
  workspaceID: string,
  capabilityID: string,
  body: UpdateCapabilityRequest
): Promise<Capability> {
  return apiRequest<Capability>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}`,
    { method: "PATCH", body }
  )
}

async function createCapabilityVersion(
  workspaceID: string,
  capabilityID: string,
  body: CreateCapabilityVersionRequest
): Promise<CapabilityVersion> {
  return apiRequest<CapabilityVersion>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/versions`,
    { method: "POST", body }
  )
}

export async function listAgentCapabilities(
  workspaceID: string | null,
  agentID: string | null
): Promise<ListAgentCapabilitiesResponse> {
  if (!workspaceID || !agentID) {
    return { workspace_id: workspaceID ?? "", agent_id: agentID ?? "", installed: [], available: [] }
  }
  const data = await apiRequest<ListAgentCapabilitiesResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/capabilities`
  )
  return { ...data, installed: data.installed.map(normalizeAgentCapability), available: data.available.map(normalizeCapability) }
}

function normalizeCapability(capability: Capability): Capability {
  return { ...capability, id: capability.id ?? capability.capability_id ?? "", latest_version: capability.latest_version ?? capability.latest_published_version }
}

function normalizeAgentCapability(item: AgentCapability): AgentCapability {
  const capability = item.capability ?? (item.name || item.type ? {
    id: item.capability_id,
    workspace_id: item.workspace_id ?? "",
    type: item.type ?? "mcp",
    name: item.name ?? `Capability ${item.capability_id.slice(0, 8)}`,
    description: item.description ?? "",
    visibility: item.visibility,
    status: item.status ?? "active",
    required_credentials: item.required_credentials,
    deprecated_at: item.deprecated_at,
    from_marketplace: true,
    source_workspace_id: item.workspace_id,
    source_workspace_name: item.source_workspace_name,
    latest_version_id: item.latest_version_id,
    latest_version: item.latest_version,
    latest_version_created_at: item.latest_version_created_at,
    pinned_version_id: item.capability_version_id,
    pinned_version: item.version,
    creator_id: "",
    created_at: item.latest_version_created_at ?? item.created_at,
    updated_at: item.latest_version_created_at ?? item.updated_at,
  } satisfies Capability : undefined)
  return { ...item, capability }
}

function noWorkspaceError(): ApiError {
  return new ApiError({ status: 0, code: "no_workspace", message: "no workspace selected" })
}

async function enableAgentCapability(
  workspaceID: string,
  agentID: string,
  capabilityVersionID: string,
  body: EnableAgentCapabilityRequest,
) {
  return apiRequest(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/capabilities/${encodeURIComponent(capabilityVersionID)}/enable`,
    { method: "POST", body },
  )
}

async function deleteAgentCapability(
  workspaceID: string,
  agentID: string,
  capabilityVersionID: string,
) {
  return apiRequest<void>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/capabilities/${encodeURIComponent(capabilityVersionID)}`,
    { method: "DELETE" },
  )
}

async function setBuiltinCapabilityEnabled(
  workspaceID: string,
  agentID: string,
  key: string,
  enabled: boolean,
) {
  return apiRequest(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/builtin-capabilities/${encodeURIComponent(key)}`,
    { method: "PUT", body: { enabled } },
  )
}

export function useCapabilitiesQuery(workspaceID: string | null, name = "", type = "", page?: number, pageSize?: number) {
  return useQuery({
    queryKey: KEY_CAPABILITIES(workspaceID ?? "_none", name.trim(), type.trim(), page, pageSize),
    queryFn: () => listCapabilities(workspaceID, name, type, page, pageSize),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useCapabilityQuery(workspaceID: string | null, capabilityID: string | null) {
  return useQuery({
    queryKey: KEY_CAPABILITY(workspaceID ?? "_none", capabilityID ?? "_none"),
    queryFn: () => getCapability(workspaceID as string, capabilityID as string),
    enabled: !!workspaceID && !!capabilityID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useCapabilityVersionsQuery(workspaceID: string | null, capabilityID: string | null) {
  return useQuery({
    queryKey: KEY_CAPABILITY_VERSIONS(workspaceID ?? "_none", capabilityID ?? "_none"),
    queryFn: () => listCapabilityVersions(workspaceID as string, capabilityID as string),
    enabled: !!workspaceID && !!capabilityID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useAgentCapabilitiesQuery(
  workspaceID: string | null,
  agentID: string | null
) {
  return useQuery({
    queryKey: KEY_AGENT_CAPABILITIES(workspaceID ?? "_none", agentID ?? "_none"),
    queryFn: () => listAgentCapabilities(workspaceID, agentID),
    enabled: !!workspaceID && !!agentID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useCreateCapability(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateCapabilityRequest) => {
      if (!workspaceID) throw noWorkspaceError()
      return createCapability(workspaceID, body)
    },
    onSuccess: (capability) => {
      void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID ?? "_none") })
      void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
      if (workspaceID) void qc.invalidateQueries({ queryKey: KEY_CAPABILITY_VERSIONS(workspaceID, capability.id) })
    },
  })
}

export function useUpdateCapability(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ capabilityID, body }: { capabilityID: string; body: UpdateCapabilityRequest }) => {
      if (!workspaceID) throw noWorkspaceError()
      return updateCapability(workspaceID, capabilityID, body)
    },
    onSuccess: (capability) => {
      void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID ?? "_none") })
      if (workspaceID) void qc.invalidateQueries({ queryKey: KEY_CAPABILITY(workspaceID, capability.id) })
    },
  })
}

export function useEnableAgentCapabilityMutation(
  workspaceID: string | null,
  agentID: string | null,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ capabilityVersionID, configuration, pinningMode }: { capabilityVersionID: string; configuration?: Record<string, unknown>; pinningMode?: "latest" | "pinned" }) => {
      if (!workspaceID || !agentID) throw new Error("workspace and agent are required")
      return enableAgentCapability(workspaceID, agentID, capabilityVersionID, { configuration, pinning_mode: pinningMode })
    },
    retry: noUnreachableRetry,
    onSuccess: () => {
      if (workspaceID && agentID) {
        qc.invalidateQueries({ queryKey: KEY_AGENT_CAPABILITIES(workspaceID, agentID) })
      }
    },
  })
}

export function aggregateRequiredCredentials(
  selectedNames: string[],
  allCapabilities: Capability[],
): RequiredCredential[] {
  return aggregateRequiredCredentialsByID(
    allCapabilities.filter((cap) => selectedNames.includes(cap.name)).map((cap) => cap.id),
    allCapabilities,
  )
}

export function aggregateRequiredCredentialsByID(
  selectedIDs: string[],
  allCapabilities: Capability[],
): RequiredCredential[] {
  const selected = new Set(selectedIDs)
  const seen = new Set<string>()
  const result: RequiredCredential[] = []
  for (const cap of allCapabilities) {
    if (!selected.has(cap.id)) continue
    if (!cap.required_credentials) continue
    for (const rc of cap.required_credentials) {
      if (rc.required && !seen.has(rc.kind)) {
        seen.add(rc.kind)
        result.push(rc)
      }
    }
  }
  return result
}

export function useDeleteAgentCapabilityMutation(
  workspaceID: string | null,
  agentID: string | null,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (capabilityVersionID: string) => {
      if (!workspaceID || !agentID) throw new Error("workspace and agent are required")
      return deleteAgentCapability(workspaceID, agentID, capabilityVersionID)
    },
    retry: noUnreachableRetry,
    onSuccess: () => {
      if (workspaceID && agentID) {
        qc.invalidateQueries({ queryKey: KEY_AGENT_CAPABILITIES(workspaceID, agentID) })
      }
    },
  })
}

export function useToggleBuiltinCapabilityMutation(
  workspaceID: string | null,
  agentID: string | null,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ key, enabled }: { key: string; enabled: boolean }) => {
      if (!workspaceID || !agentID) throw new Error("workspace and agent are required")
      return setBuiltinCapabilityEnabled(workspaceID, agentID, key, enabled)
    },
    retry: noUnreachableRetry,
    onSuccess: () => {
      if (workspaceID && agentID) {
        qc.invalidateQueries({ queryKey: KEY_AGENT_CAPABILITIES(workspaceID, agentID) })
      }
    },
  })
}
