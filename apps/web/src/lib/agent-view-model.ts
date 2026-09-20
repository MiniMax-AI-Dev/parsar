import type { Agent, AgentDetail } from "./api-types"

export function defaultModelOf(agent: Agent | AgentDetail | null | undefined): string {
  return String(agent?.config?.model ?? "—")
}

/** Shared by the list and its filter counts. */
export function searchAgents(agents: Agent[], keyword: string): Agent[] {
  const query = keyword.trim().toLowerCase()
  if (!query) return agents
  return agents.filter((agent) =>
    [agent.name, agent.description, agent.slug, defaultModelOf(agent)]
      .some(value => value.toLowerCase().includes(query)),
  )
}
