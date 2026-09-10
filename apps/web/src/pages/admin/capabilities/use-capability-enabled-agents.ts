import { useQueries } from "@tanstack/react-query"
import { KEY_AGENT_CAPABILITIES, listAgentCapabilities } from "../../../lib/api-capabilities"
import { noUnreachableRetry } from "../../../lib/api-client"
import type { Capability, CapabilityVersion } from "../../../lib/api-types"
import { agentCapabilityVersion } from "../../../lib/agent-capability-version"

interface AgentInstallation {
  agentID: string
  agentName: string
  version: string
  latest: boolean
}

export function useCapabilityEnabledAgents(wid: string | null, agents: Array<{ id: string; name: string }>, capability: Capability | null, versions: CapabilityVersion[]) {
  const queries = useQueries({
    queries: agents.map((agent) => ({
      queryKey: KEY_AGENT_CAPABILITIES(wid ?? "_none", agent.id),
      queryFn: () => listAgentCapabilities(wid, agent.id),
      enabled: !!wid && !!capability,
      retry: noUnreachableRetry,
      staleTime: 30_000,
    })),
  })
  const status = {
    isLoading: queries.some((q) => q.isLoading),
    error: queries.find((q) => q.error)?.error,
    refetch: () => Promise.all(queries.map((q) => q.refetch())),
  }
  const latest = versions[0]
  const versionCounts = new Map<string, number>()
  const installations: AgentInstallation[] = []
  if (!capability) return { installations, versionCounts, ...status }
  queries.forEach((q, idx) => {
    for (const item of q.data?.installed ?? []) {
      if (!item.enabled || item.capability_id !== capability.id) continue
      const version = agentCapabilityVersion(item, item.capability, versions)
      if (version) versionCounts.set(version.id, (versionCounts.get(version.id) ?? 0) + 1)
      installations.push({
        agentID: agents[idx].id,
        agentName: agents[idx].name,
        version: version?.version ?? "—",
        latest: !!version && version.id === latest?.id,
      })
    }
  })
  return { installations, versionCounts, ...status }
}
