import type { Agent, Model } from "./api-types"
import type { AgentEngine } from "./agent-view-model"
import { modelProtocols, modelSupportedEndpointTypes, type WireProtocol } from "./model-protocol"

export type DaemonAgentEngine = Exclude<AgentEngine, "external">

export function agentEngineFromAgent(a?: Agent | null): DaemonAgentEngine {
  const v = String((a?.config ?? {}).agent_kind ?? "claude_code")
  if (v === "opencode") return "opencode"
  if (v === "codex") return "codex"
  if (v === "mcode") return "mcode"
  if (v === "pi") return "pi"
  return "claude_code"
}

function engineSupportsProtocol(engine: DaemonAgentEngine, protocol: WireProtocol | null): boolean {
  switch (engine) {
    case "claude_code":
      return protocol === "anthropic"
    case "codex":
      return protocol === "openai"
    case "pi":
      return protocol === "anthropic" || protocol === "openai" || protocol === "google"
    case "mcode":
    case "opencode":
      return true
  }
}

export function engineSupportsModel(engine: DaemonAgentEngine, model: Model): boolean {
  const endpointTypes = modelSupportedEndpointTypes(model)
  if (endpointTypes.length > 0) {
    switch (engine) {
      case "claude_code":
        return endpointTypes.includes("anthropic")
      case "codex":
        return endpointTypes.includes("openai") || endpointTypes.includes("openai-response")
      case "pi":
        return (
          endpointTypes.includes("anthropic") ||
          endpointTypes.includes("openai") ||
          endpointTypes.includes("google_generative_ai")
        )
      case "mcode":
      case "opencode":
        return true
    }
  }
  return modelProtocols(model).some((protocol) => engineSupportsProtocol(engine, protocol))
}
