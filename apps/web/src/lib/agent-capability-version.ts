import type { AgentCapability, Capability, CapabilityVersion } from "./api-types"

export function agentCapabilityFollowsLatest(
  binding: AgentCapability | undefined,
  capability: Capability | undefined,
): boolean {
  const kind = capability?.type ?? binding?.type
  // Match the daemon resolvers: MCP and System Prompt still use stored fields.
  return binding?.pinning_mode === "latest"
    && (kind === "skill" || kind === "plugin" || kind === "bundle")
}

export function agentCapabilityVersion(
  binding: AgentCapability | undefined,
  capability: Capability | undefined,
  versions: CapabilityVersion[],
): CapabilityVersion | undefined {
  if (!binding) return undefined
  const followsLatest = agentCapabilityFollowsLatest(binding, capability)
  // The binding response's latest version respects the runtime's deprecation cutoff.
  const id = followsLatest
    ? capability?.latest_version_id ?? binding.latest_version_id
    : binding.capability_version_id
  const version = versions.find((item) => item.id === id)
  if (version) return version
  const label = followsLatest
    ? capability?.latest_version ?? binding.latest_version
    : capability?.pinned_version ?? binding.version
  return id && label ? {
    id,
    capability_id: binding.capability_id,
    version: label,
    creator_id: "",
    created_at: binding.updated_at,
  } : undefined
}
