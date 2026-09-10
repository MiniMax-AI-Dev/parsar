import { useQuery } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"

export type AgentMCPCredential = { created_at: string; expires_at: string }
export type AgentMCPTokenStatus = { credential: AgentMCPCredential | null }

export function agentMCPEndpoint(workspaceID: string, agentID: string) {
  return `/api/v1/workspaces/${workspaceID}/agents/${agentID}/mcp`
}

export function useAgentMCPToken(workspaceID: string, agentID: string, userID: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ["admin", "agentMCPToken", workspaceID, agentID, userID],
    queryFn: () => apiRequest<AgentMCPTokenStatus>(agentMCPEndpoint(workspaceID, agentID) + "-token"),
    enabled: enabled && !!userID,
    retry: noUnreachableRetry,
  })
}

export function createAgentMCPToken(workspaceID: string, agentID: string) {
  return apiRequest<{ credential: AgentMCPCredential; token: string }>(agentMCPEndpoint(workspaceID, agentID) + "-token", { method: "POST", body: {} })
}

export function revokeAgentMCPToken(workspaceID: string, agentID: string) {
  return apiRequest<void>(agentMCPEndpoint(workspaceID, agentID) + "-token", { method: "DELETE" })
}
