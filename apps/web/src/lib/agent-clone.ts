import type { AgentCapability } from "./api-types"

export function withoutCredentialBindings(config: Record<string, unknown>): Record<string, unknown> {
  const copy = { ...config }
  delete copy.credential_bindings
  delete copy.model_credential_binding
  return copy
}

export function cloneableAgentCapabilities(bindings: AgentCapability[]): AgentCapability[] {
  return bindings.filter((binding) => binding.enabled && !binding.built_in)
}
