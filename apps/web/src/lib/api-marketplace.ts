import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"
import {
  KEY_AGENT_CAPABILITIES,
  KEY_CAPABILITIES_WORKSPACE,
  KEY_CAPABILITY_VERSIONS,
} from "./api-capabilities"
import type { Capability } from "./api-types"

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

export interface MarketplaceCapabilityDetail {
  capability_id: string
  type: Capability["type"]
  version_id: string
  version: string
  git_repo_url?: string
  git_ref?: string
  path?: string
  skill?: MarketplaceSkillDetail
  mcp?: MarketplaceMCPDetail
}

export interface MarketplaceSkillDetail {
  slug: string
  title: string
  description?: string
  instruction: string
  trigger?: string
  files?: MarketplaceSkillFile[]
}

export interface MarketplaceSkillFile {
  path: string
  content: string
  kind: "markdown" | "script" | "asset"
}

export interface MarketplaceMCPDetail {
  servers: MarketplaceMCPServer[]
}

export interface MarketplaceMCPServer {
  name: string
  transport?: "stdio" | "streamable-http"
  url?: string
  command?: string
  args?: string[]
  env?: Record<string, MarketplaceMCPEnvValue>
  startup_timeout_sec?: number
}

export interface MarketplaceMCPEnvValue {
  mode: "literal" | "inline_secret" | "credential_ref"
  value?: string
  credential_kind_code?: string
  redacted?: boolean
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
  agent_name?: string
  name?: string
  capability_version_id?: string
  version?: string
}

export interface MCPDirectoryPublisher {
  name: string
  url: string
}

export interface MCPDirectoryItem {
  id: string
  name: string
  description: string
  publisher: MCPDirectoryPublisher
  icon_url?: string
  homepage_url?: string
  repository_url?: string
  verified: boolean
  categories: string[]
  popularity_rank: number
  version: string
  transport: "stdio" | "streamable-http"
  url?: string
  command?: string
  args?: string[]
  env?: string[]
  startup_timeout_sec?: number
  installed: boolean
  installed_capability_id: string | null
}

export interface MCPDirectoryListResponse {
  items: MCPDirectoryItem[]
  updated_at: string
  source: "builtin" | "remote"
}

export interface MCPDirectoryImportResponse {
  installed: boolean
  capability_id: string
  created: boolean
  capability?: Capability
}

interface MarketplaceListResponse {
  capabilities?: MarketplaceCapability[]
  marketplace?: MarketplaceCapability[]
  items?: MarketplaceCapability[]
}

interface MarketplaceDetailResponse {
  capability: MarketplaceCapabilityDetail
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

export const KEY_MARKETPLACE_LIST = (workspaceID: string) =>
  ["admin", "capabilityMarketplace", workspaceID] as const
export const KEY_MARKETPLACE_DETAIL = (workspaceID: string, capabilityID: string) =>
  ["admin", "capabilityMarketplaceDetail", workspaceID, capabilityID] as const
export const KEY_TARGET_MARKETPLACE_INSTALLS = (workspaceID: string) =>
  ["admin", "targetMarketplaceInstalls", workspaceID] as const
export const KEY_INSTALL_COUNT = (workspaceID: string, capabilityID: string) =>
  ["admin", "capabilityInstallCount", workspaceID, capabilityID] as const
export const KEY_MARKETPLACE_ENABLED_AGENTS = (workspaceID: string, capabilityID: string) =>
  ["admin", "marketplaceEnabledAgents", workspaceID, capabilityID] as const
export const KEY_MCP_DIRECTORY = (workspaceID: string) =>
  ["admin", "mcpDirectory", workspaceID] as const
export const KEY_MCP_DIRECTORY_DETAIL = (workspaceID: string, catalogID: string) =>
  ["admin", "mcpDirectoryDetail", workspaceID, catalogID] as const

async function listMarketplace(workspaceID: string | null): Promise<MarketplaceCapability[]> {
  if (!workspaceID) return []
  const data = await apiRequest<MarketplaceListResponse | MarketplaceCapability[]>(
    `/api/v1/capabilities/marketplace`,
    { query: { workspace_id: workspaceID } },
  )
  if (Array.isArray(data)) return data
  return (data.capabilities ?? data.marketplace ?? data.items ?? []).map(
    normalizeMarketplaceCapability,
  )
}

async function getMarketplaceDetail(
  workspaceID: string | null,
  capabilityID: string | null,
): Promise<MarketplaceCapabilityDetail> {
  if (!workspaceID || !capabilityID) throw new Error("workspace and capability are required")
  const data = await apiRequest<MarketplaceDetailResponse>(
    `/api/v1/capabilities/marketplace/${encodeURIComponent(capabilityID)}`,
    { query: { workspace_id: workspaceID } },
  )
  return data.capability
}

async function listTargetInstalls(workspaceID: string | null): Promise<TargetMarketplaceInstall[]> {
  if (!workspaceID) return []
  const data = await apiRequest<TargetInstallsResponse | TargetMarketplaceInstall[]>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/marketplace-installs`,
  )
  const items = Array.isArray(data)
    ? data
    : (data.capabilities ?? data.installs ?? data.items ?? [])
  return items.map(normalizeMarketplaceInstall)
}

async function getInstallCount(
  workspaceID: string | null,
  capabilityID: string | null,
): Promise<number> {
  if (!workspaceID || !capabilityID) return 0
  const data = await apiRequest<InstallCountResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/install-count`,
  )
  return data.install_count ?? data.workspace_count ?? data.count ?? 0
}

async function listEnabledAgents(
  workspaceID: string | null,
  capabilityID: string | null,
): Promise<EnabledMarketplaceAgent[]> {
  if (!workspaceID || !capabilityID) return []
  const data = await apiRequest<EnabledAgentsResponse | EnabledMarketplaceAgent[]>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/enabled-agents`,
  )
  const items = Array.isArray(data) ? data : (data.agents ?? data.items ?? [])
  return items.map(normalizeEnabledAgent)
}

async function listMCPDirectory(workspaceID: string | null): Promise<MCPDirectoryListResponse> {
  if (!workspaceID) return { items: [], updated_at: "", source: "builtin" }
  return apiRequest<MCPDirectoryListResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/mcp-directory`,
  )
}

async function getMCPDirectoryItem(
  workspaceID: string | null,
  catalogID: string | null,
): Promise<MCPDirectoryItem> {
  if (!workspaceID || !catalogID) throw new Error("workspace and catalog item are required")
  return apiRequest<MCPDirectoryItem>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/mcp-directory/${encodeURIComponent(catalogID)}`,
  )
}

async function importMCPDirectoryItem(
  workspaceID: string,
  catalogID: string,
): Promise<MCPDirectoryImportResponse> {
  return apiRequest<MCPDirectoryImportResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/mcp-directory/${encodeURIComponent(catalogID)}/import`,
    { method: "POST" },
  )
}

function normalizeMarketplaceCapability(item: MarketplaceCapability): MarketplaceCapability {
  const id = item.id ?? item.capability_id ?? ""
  return {
    ...item,
    id,
    latest_version: item.latest_version ?? item.latest_published_version,
    created_at: item.created_at ?? item.latest_version_created_at,
    updated_at: item.updated_at ?? item.latest_version_created_at,
  }
}

function normalizeMarketplaceInstall(item: TargetMarketplaceInstall): TargetMarketplaceInstall {
  return normalizeMarketplaceCapability(item) as TargetMarketplaceInstall
}

function normalizeEnabledAgent(item: EnabledMarketplaceAgent): EnabledMarketplaceAgent {
  return { ...item, name: item.name ?? item.agent_name ?? "—" }
}

async function postWorkspaceCapability(
  workspaceID: string,
  capabilityID: string,
  action: "publish" | "unpublish" | "deprecate" | "undeprecate",
) {
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

async function upgradeCapability(
  workspaceID: string,
  agentID: string,
  capabilityID: string,
  versionID: string,
) {
  return apiRequest(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/capabilities/${encodeURIComponent(capabilityID)}/upgrade`,
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

export function useMarketplaceDetail(workspaceID: string | null, capabilityID: string | null) {
  return useQuery({
    queryKey: KEY_MARKETPLACE_DETAIL(workspaceID ?? "_none", capabilityID ?? "_none"),
    queryFn: () => getMarketplaceDetail(workspaceID, capabilityID),
    enabled: !!workspaceID && !!capabilityID,
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

export function useMarketplaceEnabledAgents(
  workspaceID: string | null,
  capabilityID: string | null,
) {
  return useQuery({
    queryKey: KEY_MARKETPLACE_ENABLED_AGENTS(workspaceID ?? "_none", capabilityID ?? "_none"),
    queryFn: () => listEnabledAgents(workspaceID, capabilityID),
    enabled: !!workspaceID && !!capabilityID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useMCPDirectory(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_MCP_DIRECTORY(workspaceID ?? "_none"),
    queryFn: () => listMCPDirectory(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useMCPDirectoryDetail(workspaceID: string | null, catalogID: string | null) {
  return useQuery({
    queryKey: KEY_MCP_DIRECTORY_DETAIL(workspaceID ?? "_none", catalogID ?? "_none"),
    queryFn: () => getMCPDirectoryItem(workspaceID, catalogID),
    enabled: !!workspaceID && !!catalogID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useImportMCPDirectoryItem(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (catalogID: string) => {
      if (!workspaceID) throw new Error("workspace is required")
      return importMCPDirectoryItem(workspaceID, catalogID)
    },
    retry: noUnreachableRetry,
    onSuccess: (result, catalogID) => {
      if (!workspaceID) return
      qc.setQueryData<MCPDirectoryListResponse>(KEY_MCP_DIRECTORY(workspaceID), (current) =>
        current
          ? {
              ...current,
              items: current.items.map((item) =>
                item.id === catalogID
                  ? { ...item, installed: true, installed_capability_id: result.capability_id }
                  : item,
              ),
            }
          : current,
      )
      qc.setQueryData<MCPDirectoryItem>(
        KEY_MCP_DIRECTORY_DETAIL(workspaceID, catalogID),
        (current) =>
          current
            ? { ...current, installed: true, installed_capability_id: result.capability_id }
            : current,
      )
      void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID) })
      void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
    },
  })
}

function invalidateMarketplace(
  qc: ReturnType<typeof useQueryClient>,
  workspaceID: string | null,
  capabilityID?: string,
) {
  void qc.invalidateQueries({ queryKey: KEY_MARKETPLACE_LIST(workspaceID ?? "_none") })
  void qc.invalidateQueries({ queryKey: KEY_TARGET_MARKETPLACE_INSTALLS(workspaceID ?? "_none") })
  void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID ?? "_none") })
  void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
  if (workspaceID && capabilityID) {
    void qc.invalidateQueries({ queryKey: KEY_INSTALL_COUNT(workspaceID, capabilityID) })
    void qc.invalidateQueries({
      queryKey: KEY_MARKETPLACE_ENABLED_AGENTS(workspaceID, capabilityID),
    })
    void qc.invalidateQueries({ queryKey: KEY_CAPABILITY_VERSIONS(workspaceID, capabilityID) })
  }
}

function useWorkspaceAction(
  workspaceID: string | null,
  action: "publish" | "unpublish" | "deprecate" | "undeprecate",
) {
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

// useDelete isn't a marketplace action, but shares the capabilities route so
// it lives here. A 200 response means deleted_at was written; failures come in
// two flavours: 409 (still bound to agents, with binding_count) / 500 (other).
// Components render ApiError's built-in status + message directly, so no
// special handling is needed here.
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

export function useUpgrade(workspaceID: string | null, agentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ capabilityID, versionID }: { capabilityID: string; versionID: string }) => {
      if (!workspaceID || !agentID) throw new Error("workspace and agent are required")
      return upgradeCapability(workspaceID, agentID, capabilityID, versionID)
    },
    retry: noUnreachableRetry,
    onSuccess: () => {
      if (workspaceID && agentID) {
        void qc.invalidateQueries({ queryKey: KEY_AGENT_CAPABILITIES(workspaceID, agentID) })
      }
      void qc.invalidateQueries({ queryKey: ["admin", "targetMarketplaceInstalls"] })
    },
  })
}

export function marketplaceSourceName(
  capability: Partial<MarketplaceCapability | TargetMarketplaceInstall>,
): string {
  return capability.source_workspace_name ?? ""
}
