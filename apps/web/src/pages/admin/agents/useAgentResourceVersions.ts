import { useCapabilityVersionsQuery } from "../../../lib/api-capabilities"
import { useMarketplaceList } from "../../../lib/api-marketplace"
import type { CapabilityVersion } from "../../../lib/api-types"
import type { AgentResourceSelection } from "./useAgentResourceDraft"

type ResourceVersion = Pick<CapabilityVersion, "id" | "version" | "required_credentials" | "source_payload">

export function useAgentResourceVersions(workspaceID: string | null, item: AgentResourceSelection) {
  const foreign = item.capability.workspace_id !== workspaceID
  const owned = useCapabilityVersionsQuery(foreign ? null : workspaceID, item.capability.id)
  const published = useMarketplaceList(foreign ? workspaceID : null)
  if (!foreign) return { versions: owned.data?.versions ?? [], error: owned.error, loading: owned.isLoading }
  // Consumers may use their existing pin and the currently published version,
  // without reading the source workspace's private revision history.
  const versions: ResourceVersion[] = []
  const cap = item.capability
  if (cap.pinned_version_id) versions.push({ id: cap.pinned_version_id, version: cap.pinned_version ?? cap.pinned_version_id, required_credentials: cap.required_credentials })
  const latest = published.data?.find(row => row.id === cap.id)
  if (latest?.latest_version_id && !versions.some(row => row.id === latest.latest_version_id)) {
    versions.push({ id: latest.latest_version_id, version: latest.latest_version ?? latest.latest_version_id, required_credentials: latest.required_credentials })
  }
  return { versions, error: published.error, loading: published.isLoading, latestVersionID: latest?.latest_version_id }
}
