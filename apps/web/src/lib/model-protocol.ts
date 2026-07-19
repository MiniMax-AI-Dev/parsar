import type { Model } from "./api-types"

export type WireProtocol = "anthropic" | "openai" | "google"
export type SupportedEndpointType =
  | "anthropic"
  | "openai"
  | "openai-response"
  | "google_generative_ai"

function normalizeEndpointType(raw: string): SupportedEndpointType | null {
  const value = raw.trim().toLowerCase().replace(/[-./]/g, "_")
  switch (value) {
    case "message":
    case "messages":
    case "anthropic":
    case "anthropic_message":
    case "anthropic_messages":
      return "anthropic"
    case "openai":
    case "chat":
    case "chat_completion":
    case "chat_completions":
    case "openai_chat":
    case "openai_chat_completions":
      return "openai"
    case "openai_response":
    case "openai_responses":
    case "response":
    case "responses":
      return "openai-response"
    case "google":
    case "gemini":
    case "google_generative_ai":
      return "google_generative_ai"
    default:
      return null
  }
}

export function modelSupportedEndpointTypes(
  model: Pick<Model, "config">,
): SupportedEndpointType[] {
  const raw = model.config?.supported_endpoint_types
  if (!Array.isArray(raw)) return []
  const out: SupportedEndpointType[] = []
  const seen = new Set<SupportedEndpointType>()
  for (const item of raw) {
    if (typeof item !== "string") continue
    const normalized = normalizeEndpointType(item)
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    out.push(normalized)
  }
  return out
}

export function modelProtocols(model: Pick<Model, "provider_type" | "adapter" | "config">): WireProtocol[] {
  const endpointTypes = modelSupportedEndpointTypes(model)
  const protocols: WireProtocol[] = []
  const seen = new Set<WireProtocol>()
  function add(protocol: WireProtocol) {
    if (seen.has(protocol)) return
    seen.add(protocol)
    protocols.push(protocol)
  }
  for (const endpointType of endpointTypes) {
    if (endpointType === "anthropic") add("anthropic")
    else if (endpointType === "openai" || endpointType === "openai-response") add("openai")
    else if (endpointType === "google_generative_ai") add("google")
  }
  if (protocols.length === 0) {
    const legacy = legacyModelProtocol(model)
    if (legacy) add(legacy)
  }
  return protocols
}

/** Classify a model's wire protocol from its provider_type / adapter.
 * Mirrors the allow-lists in
 * server/internal/connector/agentdaemon/model_injection.go
 * (isAnthropicRuntime / isOpenAICompatibleRuntime / piAPIProtocol) — keep the
 * case values in sync so the UI filter matches what the daemon will accept. */
export function modelProtocol(m: Pick<Model, "provider_type" | "adapter" | "config">): WireProtocol | null {
  return modelProtocols(m)[0] ?? null
}

function legacyModelProtocol(m: Pick<Model, "provider_type" | "adapter">): WireProtocol | null {
  for (const v of [m.provider_type, m.adapter]) {
    switch (v.trim().toLowerCase()) {
      case "anthropic":
      case "anthropic-compatible":
      case "anthropic_compatible":
      case "@ai-sdk/anthropic":
        return "anthropic"
      case "openai":
      case "openai-compatible":
      case "openai_compatible":
      case "azure-openai":
      case "azure_openai":
      case "@ai-sdk/openai":
      case "@ai-sdk/openai-compatible":
      case "@ai-sdk/azure":
        return "openai"
      case "google":
      case "gemini":
      case "google-generative-ai":
      case "google_generative_ai":
      case "@ai-sdk/google":
        return "google"
    }
  }
  return null
}

/** Display label for a model's own wire protocol. null protocol = unknown
 * adapter, shown as a dash. */
export function protocolLabel(protocol: WireProtocol | null): string {
  switch (protocol) {
    case "anthropic":
      return "Anthropic"
    case "openai":
      return "OpenAI"
    case "google":
      return "Google"
    default:
      return "—"
  }
}

export function protocolListLabel(protocols: WireProtocol[]): string {
  if (protocols.length === 0) return "—"
  return protocols.map((protocol) => protocolLabel(protocol)).join(" + ")
}
