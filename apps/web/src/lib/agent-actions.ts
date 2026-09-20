import { requestStartSession } from "./core-api"
import type { Agent, UserWorkspace } from "./api-types"

export function agentActionPermissions(role?: UserWorkspace["role"]) {
  const canManage = role === "owner" || role === "admin"
  return { canManage, canChat: canManage || role === "member" }
}

export function useAgentChat(_workspaceID: string | null, canChat: boolean) {
  function startChat(agent: Agent) {
    if (canChat && agent.status === "active") requestStartSession(agent.id)
  }
  return { startChat, pendingID: null }
}
