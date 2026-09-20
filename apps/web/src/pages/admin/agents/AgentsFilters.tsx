import { useTranslation } from "react-i18next"
import { FilterGroup, FilterMenu, FilterOption } from "../../../components/ui/filter-menu"
import type { Agent } from "../../../lib/api-types"

export type AgentStatusFilter = Agent["status"] | ""
const STATUSES: Agent["status"][] = ["active", "disabled", "error"]

export function AgentsFilters({ agents, statusFilter, onStatusChange }: {
  agents: Agent[]
  statusFilter: AgentStatusFilter
  onStatusChange: (value: AgentStatusFilter) => void
}) {
  const { t } = useTranslation("admin")
  return <FilterMenu label={t("agents.filters.label")} summary={statusFilter ? t(`agents.status.${statusFilter}`) : null}>
    <FilterGroup value={statusFilter} onValueChange={value => onStatusChange(value as AgentStatusFilter)}>
      <FilterOption value="" label={t("agents.filters.allStatuses")} count={agents.length} />
      {STATUSES.map(status => <FilterOption key={status} value={status} label={t(`agents.status.${status}`)} count={agents.filter(agent => agent.status === status).length} />)}
    </FilterGroup>
  </FilterMenu>
}
