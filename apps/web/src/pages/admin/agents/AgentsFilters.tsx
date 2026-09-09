import { useTranslation } from "react-i18next"
import { FilterGroup, FilterMenu, FilterOption, FilterSeparator } from "../../../components/ui/filter-menu"
import { AGENT_CONNECTOR_TYPES, agentConnectorKey, agentConnectorLabel } from "../../../lib/agent-view-model"
import type { Agent } from "../../../lib/api-types"

export type AgentStatusFilter = Agent["status"] | ""
const STATUSES: Agent["status"][] = ["active", "disabled", "error"]

export function AgentsFilters({ agents, connectorFilter, statusFilter, onConnectorChange, onStatusChange, canManage }: {
  agents: Agent[]
  connectorFilter: string
  statusFilter: AgentStatusFilter
  onConnectorChange: (value: string) => void
  onStatusChange: (value: AgentStatusFilter) => void
  canManage: boolean
}) {
  const { t } = useTranslation("admin")
  const statusMatches = agents.filter((agent) => !statusFilter || agent.status === statusFilter)
  const connectorMatches = agents.filter((agent) => !connectorFilter || agentConnectorKey(agent.connector_type) === connectorFilter)
  const counts = new Map<string, number>(AGENT_CONNECTOR_TYPES.map((key) => [key, 0]))
  for (const agent of statusMatches) {
    const key = agentConnectorKey(agent.connector_type)
    counts.set(key, (counts.get(key) ?? 0) + 1)
  }
  const summary = [statusFilter && t(`agents.status.${statusFilter}`), connectorFilter && agentConnectorLabel(connectorFilter)].filter(Boolean).join(" · ")

  return (
    <FilterMenu label={t("agents.filters.label")} summary={summary || null}>
      {canManage && <>
        <FilterGroup value={statusFilter} onValueChange={(value) => onStatusChange(value as AgentStatusFilter)}>
          <FilterOption value="" label={t("agents.filters.allStatuses")} count={connectorMatches.length} />
          {STATUSES.map((status) => <FilterOption key={status} value={status} label={t(`agents.status.${status}`)} count={connectorMatches.filter((agent) => agent.status === status).length} />)}
        </FilterGroup>
        <FilterSeparator />
      </>}
      <FilterGroup value={connectorFilter} onValueChange={onConnectorChange}>
        <FilterOption value="" label={t("agents.filters.allConnectors")} count={statusMatches.length} />
        {[...counts].map(([connector, count]) => <FilterOption key={connector} value={connector} label={agentConnectorLabel(connector)} count={count} />)}
      </FilterGroup>
    </FilterMenu>
  )
}
