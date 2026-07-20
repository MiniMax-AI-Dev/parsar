import type { Model } from "./api-types"
import {
  endpointBaseURLsForProvider,
  endpointTypeForProtocol,
  type ProviderTypeOption,
} from "./model-provider-options"
import { modelSupportedEndpointTypes, type SupportedEndpointType } from "./model-protocol"

export type EditableEndpointType = "anthropic" | "openai" | "openai-response"

const EDITABLE_ENDPOINT_TYPES: EditableEndpointType[] = [
  "anthropic",
  "openai",
  "openai-response",
]

export interface EndpointBaseURLRow {
  endpointType: EditableEndpointType
  baseURL: string
}

export function endpointTypeDisplayLabel(endpointType: string): string {
  switch (endpointType) {
    case "anthropic":
      return "Anthropic Messages"
    case "openai":
      return "OpenAI Chat Completions"
    case "openai-response":
      return "OpenAI Responses"
    default:
      return endpointType
  }
}

export function editableEndpointTypesForModel(
  model: Pick<Model, "config" | "adapter">,
  provider: ProviderTypeOption | undefined,
): EditableEndpointType[] {
  const supported = modelSupportedEndpointTypes(model)
  const fromSupported = supported.filter(isEditableEndpointType)
  if (fromSupported.length > 0) return uniqueEndpointTypes(fromSupported)

  const fromProvider = (provider?.protocols ?? [])
    .map((protocol) => endpointTypeForProtocol(protocol.id))
    .filter(isEditableEndpointType)
  if (fromProvider.length > 0) {
    const out = uniqueEndpointTypes(fromProvider)
    if (out.includes("openai") && !out.includes("openai-response")) {
      out.push("openai-response")
    }
    return out
  }

  if (model.adapter.includes("anthropic")) return ["anthropic"]
  if (model.adapter.includes("openai")) return ["openai", "openai-response"]
  return []
}

export function endpointBaseURLRowsForModel(
  model: Pick<Model, "config" | "base_url" | "adapter">,
  provider: ProviderTypeOption | undefined,
): EndpointBaseURLRow[] {
  const endpointTypes = editableEndpointTypesForModel(model, provider)
  const saved = endpointBaseURLsFromConfig(model.config)
  const inferred = endpointBaseURLsForProvider(provider, model.base_url)
  return endpointTypes.map((endpointType) => ({
    endpointType,
    baseURL: saved[endpointType] ?? inferred[endpointType] ?? model.base_url,
  }))
}

export function endpointBaseURLsFromRows(
  rows: EndpointBaseURLRow[],
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const row of rows) {
    const baseURL = row.baseURL.trim()
    if (baseURL) out[row.endpointType] = baseURL
  }
  return out
}

function endpointBaseURLsFromConfig(config: Record<string, unknown> | undefined): Record<string, string> {
  const raw = config?.endpoint_base_urls
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {}
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value !== "string" || value.trim() === "") continue
    if (!isEditableEndpointType(key)) continue
    out[key] = value.trim()
  }
  return out
}

function isEditableEndpointType(value: string | null): value is EditableEndpointType {
  return !!value && EDITABLE_ENDPOINT_TYPES.includes(value as EditableEndpointType)
}

function uniqueEndpointTypes(values: SupportedEndpointType[]): EditableEndpointType[] {
  const out: EditableEndpointType[] = []
  const seen = new Set<string>()
  for (const value of values) {
    if (!isEditableEndpointType(value) || seen.has(value)) continue
    seen.add(value)
    out.push(value)
  }
  return out
}
