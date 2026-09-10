import type { AgentCapability, Capability } from "./api-types"
import { normalizeMarketplaceCapability } from "./api-marketplace"

export function cloneMarketplaceCapabilities(capabilities: Capability[]): Capability[] {
  return [...new Map(capabilities.map((capability) => {
    const cap = normalizeMarketplaceCapability(capability)
    return [cap.id, { ...cap, from_marketplace: true }]
  })).values()]
}

export function withoutCredentialBindings(config: Record<string, unknown>): Record<string, unknown> {
  const copy = { ...config }
  delete copy.credential_bindings
  delete copy.model_credential_binding
  return copy
}

export function cloneableAgentCapabilities(bindings: AgentCapability[]): AgentCapability[] {
  return bindings.filter((binding) => binding.enabled && !binding.built_in)
}
