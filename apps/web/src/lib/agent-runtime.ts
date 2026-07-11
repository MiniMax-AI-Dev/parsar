import type { Agent } from "./api-types"

export function agentExecutionPlacement(agent: Agent | undefined | null): "local" | "sandbox" | "unknown" {
  if (!agent) return "unknown"
  if (agent.runtime === "local") return "local"
  if (agent.runtime === "sandbox") return "sandbox"
  const config = agent.config ?? {}
  const mode = String(config.daemon_mode ?? "")
  if (mode === "sandbox") return "sandbox"
  if (mode === "local") return "local"
  return "unknown"
}

export function agentNeedsSandbox(agent: Agent | undefined | null): boolean {
  return agent?.connector_type === "agent_daemon" && agentExecutionPlacement(agent) === "sandbox"
}
