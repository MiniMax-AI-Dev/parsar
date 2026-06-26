import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"
import { KEY_AGENT_CAPABILITIES, KEY_CAPABILITIES_WORKSPACE, KEY_CAPABILITY_VERSIONS } from "./api-capabilities"
import type { Capability, CapabilityVersion, ProjectAgent } from "./api-types"

export interface MarketplaceCapability extends Capability {
  capability_id?: string
  visibility?: "workspace" | "public"
  deprecated_at?: string | null
  source_workspace_id?: string
  source_workspace_name?: string
  latest_version_id?: string
  latest_version?: string
  latest_published_version?: string
  latest_version_created_at?: string
  installed_agent_count?: number
  installed_workspace_count?: number
  install_count?: number
  enabled_agent_count?: number
  self_published?: boolean
  installed?: boolean
  from_marketplace?: boolean
  pinned_version_id?: string
  pinned_version?: string
}

export interface TargetMarketplaceInstall extends MarketplaceCapability {
  source_workspace_id: string
  source_workspace_name: string
  pinned_version_id: string
  pinned_version: string
  latest_version_id?: string
  latest_version?: string
  enabled_agent_count: number
  enabled_agents?: EnabledMarketplaceAgent[]
}

export interface EnabledMarketplaceAgent {
  id?: string
  agent_id?: string
  project_agent_id?: string
  agent_name?: string
  name?: string
  capability_version_id?: string
  version?: string
}

interface MarketplaceListResponse {
  capabilities?: MarketplaceCapability[]
  marketplace?: MarketplaceCapability[]
  items?: MarketplaceCapability[]
}

interface TargetInstallsResponse {
  capabilities?: TargetMarketplaceInstall[]
  installs?: TargetMarketplaceInstall[]
  items?: TargetMarketplaceInstall[]
}

interface InstallCountResponse {
  count?: number
  install_count?: number
  workspace_count?: number
}

interface EnabledAgentsResponse {
  agents?: EnabledMarketplaceAgent[]
  items?: EnabledMarketplaceAgent[]
}

export const KEY_MARKETPLACE_LIST = (workspaceID: string) => ["admin", "capabilityMarketplace", workspaceID] as const
export const KEY_TARGET_MARKETPLACE_INSTALLS = (workspaceID: string) => ["admin", "targetMarketplaceInstalls", workspaceID] as const
export const KEY_INSTALL_COUNT = (workspaceID: string, capabilityID: string) => ["admin", "capabilityInstallCount", workspaceID, capabilityID] as const
export const KEY_MARKETPLACE_ENABLED_AGENTS = (workspaceID: string, capabilityID: string) => ["admin", "marketplaceEnabledAgents", workspaceID, capabilityID] as const

async function listMarketplace(workspaceID: string | null): Promise<MarketplaceCapability[]> {
  if (!workspaceID) return []
  const data = await apiRequest<MarketplaceListResponse | MarketplaceCapability[]>(
    `/api/v1/capabilities/marketplace`,
    { query: { workspace_id: workspaceID } },
  )
  if (Array.isArray(data)) return data
  return (data.capabilities ?? data.marketplace ?? data.items ?? []).map(normalizeMarketplaceCapability)
}

async function listTargetInstalls(workspaceID: string | null): Promise<TargetMarketplaceInstall[]> {
  if (!workspaceID) return []
  const data = await apiRequest<TargetInstallsResponse | TargetMarketplaceInstall[]>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/marketplace-installs`,
  )
  const items = Array.isArray(data) ? data : data.capabilities ?? data.installs ?? data.items ?? []
  return items.map(normalizeMarketplaceInstall)
}

async function getInstallCount(workspaceID: string | null, capabilityID: string | null): Promise<number> {
  if (!workspaceID || !capabilityID) return 0
  const data = await apiRequest<InstallCountResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/install-count`,
  )
  return data.install_count ?? data.workspace_count ?? data.count ?? 0
}

async function listEnabledAgents(workspaceID: string | null, capabilityID: string | null): Promise<EnabledMarketplaceAgent[]> {
  if (!workspaceID || !capabilityID) return []
  const data = await apiRequest<EnabledAgentsResponse | EnabledMarketplaceAgent[]>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/enabled-agents`,
  )
  const items = Array.isArray(data) ? data : data.agents ?? data.items ?? []
  return items.map(normalizeEnabledAgent)
}

function normalizeMarketplaceCapability(item: MarketplaceCapability): MarketplaceCapability {
  const id = item.id ?? item.capability_id ?? ""
  return { ...item, id, latest_version: item.latest_version ?? item.latest_published_version, created_at: item.created_at ?? item.latest_version_created_at, updated_at: item.updated_at ?? item.latest_version_created_at }
}

function normalizeMarketplaceInstall(item: TargetMarketplaceInstall): TargetMarketplaceInstall {
  return normalizeMarketplaceCapability(item) as TargetMarketplaceInstall
}

function normalizeEnabledAgent(item: EnabledMarketplaceAgent): EnabledMarketplaceAgent {
  return { ...item, name: item.name ?? item.agent_name ?? "—" }
}

async function postWorkspaceCapability(workspaceID: string, capabilityID: string, action: "publish" | "unpublish" | "deprecate" | "undeprecate") {
  return apiRequest<Capability>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/${action}`,
    { method: "POST" },
  )
}

async function uninstallMarketplace(workspaceID: string, capabilityID: string) {
  return apiRequest<{ uninstalled_agent_count?: number }>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/uninstall`,
    { method: "POST", body: { source_capability_id: capabilityID } },
  )
}

async function deleteWorkspaceCapability(workspaceID: string, capabilityID: string) {
  return apiRequest<Capability>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}`,
    { method: "DELETE" },
  )
}

async function upgradeCapability(projectID: string, projectAgentID: string, capabilityID: string, versionID: string) {
  return apiRequest(
    `/api/v1/projects/${encodeURIComponent(projectID)}/agents/${encodeURIComponent(projectAgentID)}/capabilities/${encodeURIComponent(capabilityID)}/upgrade`,
    { method: "POST", body: { new_version_id: versionID } },
  )
}

export function useMarketplaceList(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_MARKETPLACE_LIST(workspaceID ?? "_none"),
    queryFn: () => listMarketplace(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useTargetMarketplaceInstalls(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_TARGET_MARKETPLACE_INSTALLS(workspaceID ?? "_none"),
    queryFn: () => listTargetInstalls(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useInstallCount(workspaceID: string | null, capabilityID: string | null) {
  return useQuery({
    queryKey: KEY_INSTALL_COUNT(workspaceID ?? "_none", capabilityID ?? "_none"),
    queryFn: () => getInstallCount(workspaceID, capabilityID),
    enabled: !!workspaceID && !!capabilityID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useMarketplaceEnabledAgents(workspaceID: string | null, capabilityID: string | null) {
  return useQuery({
    queryKey: KEY_MARKETPLACE_ENABLED_AGENTS(workspaceID ?? "_none", capabilityID ?? "_none"),
    queryFn: () => listEnabledAgents(workspaceID, capabilityID),
    enabled: !!workspaceID && !!capabilityID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

function invalidateMarketplace(qc: ReturnType<typeof useQueryClient>, workspaceID: string | null, capabilityID?: string) {
  void qc.invalidateQueries({ queryKey: KEY_MARKETPLACE_LIST(workspaceID ?? "_none") })
  void qc.invalidateQueries({ queryKey: KEY_TARGET_MARKETPLACE_INSTALLS(workspaceID ?? "_none") })
  void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID ?? "_none") })
  void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
  if (workspaceID && capabilityID) {
    void qc.invalidateQueries({ queryKey: KEY_INSTALL_COUNT(workspaceID, capabilityID) })
    void qc.invalidateQueries({ queryKey: KEY_MARKETPLACE_ENABLED_AGENTS(workspaceID, capabilityID) })
    void qc.invalidateQueries({ queryKey: KEY_CAPABILITY_VERSIONS(workspaceID, capabilityID) })
  }
}

function useWorkspaceAction(workspaceID: string | null, action: "publish" | "unpublish" | "deprecate" | "undeprecate") {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (capabilityID: string) => {
      if (!workspaceID) throw new Error("workspace is required")
      return postWorkspaceCapability(workspaceID, capabilityID, action)
    },
    retry: noUnreachableRetry,
    onSuccess: (capability) => invalidateMarketplace(qc, workspaceID, capability.id),
  })
}

export function usePublish(workspaceID: string | null) {
  return useWorkspaceAction(workspaceID, "publish")
}

export function useUnpublish(workspaceID: string | null) {
  return useWorkspaceAction(workspaceID, "unpublish")
}

export function useDeprecate(workspaceID: string | null) {
  return useWorkspaceAction(workspaceID, "deprecate")
}

export function useUndeprecate(workspaceID: string | null) {
  return useWorkspaceAction(workspaceID, "undeprecate")
}

// useDelete 不是 marketplace 动作,但走同一条 capabilities 路由,所以挂这里。
// 后端返回 200 即代表已写 deleted_at;失败有两类:409(还有 agent 在用,带
// binding_count) / 500(其他)。组件层用 ApiError 自带的 status + message
// 直接展示,无需在这里特殊处理。
export function useDelete(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (capabilityID: string) => {
      if (!workspaceID) throw new Error("workspace is required")
      return deleteWorkspaceCapability(workspaceID, capabilityID)
    },
    retry: noUnreachableRetry,
    onSuccess: (capability) => invalidateMarketplace(qc, workspaceID, capability.id),
  })
}

export function useUninstall(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (capabilityID: string) => {
      if (!workspaceID) throw new Error("workspace is required")
      return uninstallMarketplace(workspaceID, capabilityID)
    },
    retry: noUnreachableRetry,
    onSuccess: (_result, capabilityID) => {
      invalidateMarketplace(qc, workspaceID, capabilityID)
      void qc.invalidateQueries({ queryKey: ["admin", "agentCapabilities"] })
    },
  })
}

export function useUpgrade(projectID: string | null, projectAgentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ capabilityID, versionID }: { capabilityID: string; versionID: string }) => {
      if (!projectID || !projectAgentID) throw new Error("project and agent are required")
      return upgradeCapability(projectID, projectAgentID, capabilityID, versionID)
    },
    retry: noUnreachableRetry,
    onSuccess: () => {
      if (projectID && projectAgentID) {
        void qc.invalidateQueries({ queryKey: KEY_AGENT_CAPABILITIES(projectID, projectAgentID) })
      }
      void qc.invalidateQueries({ queryKey: ["admin", "targetMarketplaceInstalls"] })
    },
  })
}

export function marketplaceSourceName(capability: Partial<MarketplaceCapability | TargetMarketplaceInstall>): string {
  return capability.source_workspace_name ?? ""
}

export function pinnedVersionOf(capability: Partial<MarketplaceCapability | TargetMarketplaceInstall>): string | undefined {
  return capability.pinned_version ?? capability.latest_version
}

export function latestVersionOf(capability: Partial<MarketplaceCapability | TargetMarketplaceInstall>, versions?: CapabilityVersion[]): CapabilityVersion | undefined {
  if (capability.latest_version_id && versions) {
    const byID = versions.find((version) => version.id === capability.latest_version_id)
    if (byID) return byID
  }
  return versions?.[0]
}

export function agentDisplayID(agent: ProjectAgent | EnabledMarketplaceAgent): string {
  if ("project_agent_id" in agent && agent.project_agent_id) return agent.project_agent_id
  if ("agent_id" in agent && agent.agent_id) return agent.agent_id
  if ("id" in agent && agent.id) return agent.id
  return ""
}
