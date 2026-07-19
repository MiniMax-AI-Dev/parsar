import {
  PROVIDER_CATALOG,
  type ModelPreset,
  type ProtocolPreset,
  type ProviderPreset,
} from "./model-presets"

export interface ProviderTypeOption {
  key: string
  adapter: string
  defaultBaseURL: string
  customHeaders: boolean
  authSchemeSelector: boolean
  label?: string
  labelKey?: string
  models?: ModelPreset[]
  protocols: ProtocolPreset[]
}

export const GATEWAY_PROVIDER_TYPES: ProviderTypeOption[] = [
  {
    key: "anthropic-compatible",
    adapter: "@ai-sdk/anthropic",
    defaultBaseURL: "",
    customHeaders: true,
    authSchemeSelector: true,
    labelKey: "models.createProvider.providerTypeLabel.anthropicCompatible",
    protocols: [{ id: "anthropic", adapter: "@ai-sdk/anthropic", baseURL: "" }],
  },
  {
    key: "openai-compatible",
    adapter: "@ai-sdk/openai-compatible",
    defaultBaseURL: "",
    customHeaders: true,
    authSchemeSelector: false,
    labelKey: "models.createProvider.providerTypeLabel.openaiCompatible",
    protocols: [{ id: "openai", adapter: "@ai-sdk/openai-compatible", baseURL: "" }],
  },
]

export function providerTypesFromCatalog(catalog: ProviderPreset[]): ProviderTypeOption[] {
  return [
    ...catalog.map((p) => ({
      key: p.key,
      adapter: p.adapter,
      defaultBaseURL: p.defaultBaseURL,
      customHeaders: p.customHeaders,
      authSchemeSelector: p.authSchemeSelector,
      label: p.name,
      models: p.models,
      protocols: p.protocols,
    })),
    ...GATEWAY_PROVIDER_TYPES,
  ]
}

export const FALLBACK_PROVIDER_TYPES = providerTypesFromCatalog(PROVIDER_CATALOG)

export function defaultModelFor(provider: ProviderTypeOption | undefined): ModelPreset | undefined {
  return provider?.models?.[0]
}

export function resolveProtocol(
  provider: ProviderTypeOption | undefined,
  protocolID: string,
): ProtocolPreset | undefined {
  if (!provider) return undefined
  return provider.protocols.find((p) => p.id === protocolID) ?? provider.protocols[0]
}

export function isKnownProviderURL(provider: ProviderTypeOption | undefined, url: string): boolean {
  if (!provider) return false
  return provider.protocols.some((p) => p.baseURL === url)
}

export function protocolIDForSeed(
  provider: ProviderTypeOption | undefined,
  seed: { base_url: string; adapter: string },
): string {
  const protocols = provider?.protocols ?? []
  if (protocols.length === 0) return "openai"
  const url = seed.base_url.trim()
  const byURL = protocols.find((p) => p.baseURL === url)
  if (byURL) return byURL.id
  const byAdapter = protocols.find((p) => p.adapter === seed.adapter.trim())
  if (byAdapter) return byAdapter.id
  return protocols[0].id
}

export function protocolDisplayLabel(id: string): string {
  switch (id) {
    case "anthropic":
      return "Anthropic"
    case "openai":
      return "OpenAI"
    case "google":
      return "Google"
    default:
      return id
  }
}

export function endpointTypeForProtocol(id: string): string | null {
  switch (id) {
    case "anthropic":
      return "anthropic"
    case "openai":
      return "openai"
    case "google":
      return "google_generative_ai"
    default:
      return null
  }
}

export function findProviderModel(
  provider: ProviderTypeOption | undefined,
  modelKey: string,
): ModelPreset | undefined {
  const key = modelKey.trim()
  if (!key) return undefined
  return provider?.models?.find((model) => model.id === key)
}

export function shouldReplaceProviderModelKey(
  modelKey: string,
  provider: ProviderTypeOption | undefined,
): boolean {
  return modelKey.trim() === "" || !!findProviderModel(provider, modelKey)
}

export function shouldReplaceModelName(name: string, model: ModelPreset | undefined): boolean {
  const current = name.trim()
  return current === "" || (!!model && current === model.name)
}
