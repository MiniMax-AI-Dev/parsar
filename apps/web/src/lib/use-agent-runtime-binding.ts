import { agentExecutionPlacement } from "./agent-runtime"
import type { Agent } from "./api-types"
import { useWorkspaceRuntimes } from "./api-runtimes"

export function useAgentRuntimeBinding(agent: Agent) {
  const explicitID = agent.runtime_id?.trim() ?? ""
  const legacyID = agent.connector_type === "agent_daemon"
    && agentExecutionPlacement(agent) === "local"
    && typeof agent.config?.device_id === "string"
    ? agent.config.device_id.trim()
    : ""
  const id = explicitID || legacyID
  const { data: runtimes } = useWorkspaceRuntimes(
    !explicitID && legacyID ? agent.workspace_id : "",
    undefined,
    { staleTime: 5_000 },
  )
  const legacy = runtimes?.find((runtime) => runtime.id === legacyID && runtime.type === "agent_daemon")

  return {
    id,
    name: (explicitID ? agent.runtime_name : legacy?.name)?.trim() || id,
    liveness: explicitID ? agent.runtime_liveness : legacy?.liveness,
  }
}
