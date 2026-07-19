import type { Model } from "./api-types"

export type WireProtocol = "anthropic" | "openai" | "google"

/** Classify a model's wire protocol from its provider_type / adapter.
 * Mirrors the allow-lists in
 * server/internal/connector/agentdaemon/model_injection.go
 * (isAnthropicRuntime / isOpenAICompatibleRuntime / piAPIProtocol) — keep the
 * case values in sync so the UI filter matches what the daemon will accept. */
export function modelProtocol(m: Pick<Model, "provider_type" | "adapter">): WireProtocol | null {
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
