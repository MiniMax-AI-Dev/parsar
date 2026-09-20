import { useState } from "react"
import { useAgentCapabilitiesQuery, useCapabilitiesQuery } from "../../../lib/api-capabilities"
import { normalizeMarketplaceCapability, normalizeMarketplaceInstall, useMarketplaceList, type TargetMarketplaceInstall } from "../../../lib/api-marketplace"
import type { Capability, InitialAgentCapabilityRequest } from "../../../lib/api-types"

export interface AgentResourceSelection extends InitialAgentCapabilityRequest { capability: Capability }

export function useAgentResourceDraft(workspaceID: string | null, agentID?: string) {
  const installed = useAgentCapabilitiesQuery(workspaceID, agentID ?? null)
  const catalog = useCapabilitiesQuery(workspaceID)
  const marketplace = useMarketplaceList(workspaceID)
  const [selection, setSelection] = useState<AgentResourceSelection[] | null>(agentID ? null : [])
  if (selection === null && installed.data) {
    setSelection(installed.data.installed.filter(item => !item.built_in && item.enabled).map(item => ({
      capability_version_id: item.capability_version_id,
      configuration: item.configuration ?? {},
      pinning_mode: item.pinning_mode ?? "pinned",
      capability: item.capability ?? { id: item.capability_id, name: item.capability_id, type: "skill", status: "disabled", workspace_id: workspaceID ?? "", description: "", creator_id: "", created_at: "", updated_at: "" },
    })))
  }
  const available = new Map((catalog.data?.capabilities ?? []).map(item => [item.id, item]))
  for (const item of catalog.data?.marketplace_installs ?? []) {
    const capability = normalizeMarketplaceInstall(item as TargetMarketplaceInstall)
    available.set(capability.id, { ...capability, status: marketplace.data?.find(row => row.id === capability.id)?.status ?? "disabled" })
  }
  for (const item of installed.data?.available ?? []) {
    const capability = normalizeMarketplaceCapability(item)
    available.set(capability.id, capability)
  }
  const add = (capability: Capability) => {
    const versionID = capability.pinned_version_id ?? capability.latest_version_id
    if (!versionID) return
    capability = { ...capability, pinned_version_id: versionID, pinned_version: capability.pinned_version ?? capability.latest_version }
    setSelection(current => current?.some(item => item.capability.id === capability.id) ? current : [...(current ?? []), { capability, capability_version_id: versionID, pinning_mode: "pinned", configuration: {} }])
  }
  return { bindings: installed.data?.installed ?? [], selection, setSelection, add, available: [...available.values()], error: installed.error ?? catalog.error ?? marketplace.error, loading: selection === null || catalog.isLoading || marketplace.isLoading, retry: () => { void installed.refetch(); void catalog.refetch(); void marketplace.refetch() } }
}
