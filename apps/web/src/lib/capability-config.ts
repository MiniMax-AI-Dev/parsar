import type { Capability, CapabilityVersion } from "./api-types"
import { useCapabilityVersionsQuery } from "./api-capabilities"

function latestCapabilityVersion(capability: Capability): CapabilityVersion | undefined {
  return capability.latest_version_id
    ? {
        id: capability.latest_version_id,
        capability_id: capability.id,
        version: capability.latest_version ?? capability.latest_published_version ?? "—",
        created_at: capability.latest_version_created_at ?? capability.created_at ?? new Date().toISOString(),
      } as CapabilityVersion
    : undefined
}

export function requiredCredentialKinds(capability: Capability) {
  return (capability.required_credentials ?? []).filter((rc) => rc.required)
}

export function catalogIDFromVersion(version: CapabilityVersion | undefined) {
  const payload = version?.source_payload
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return ""
  const catalogID = (payload as Record<string, unknown>).catalog_id
  return typeof catalogID === "string" ? catalogID.trim() : ""
}

export function useCapabilityVersions(
  workspaceID: string | null,
  capability: Capability | undefined,
  enabled: boolean,
) {
  const fromMarketplace = capability?.from_marketplace === true
  const versionsQ = useCapabilityVersionsQuery(workspaceID, enabled && !fromMarketplace ? capability?.id ?? null : null)
  const published = capability ? latestCapabilityVersion(capability) : undefined
  const versions = fromMarketplace ? (published ? [published] : []) : versionsQ.data?.versions ?? []
  const latest = versions[0] ?? published
  return { latest, versions, versionsQ }
}
