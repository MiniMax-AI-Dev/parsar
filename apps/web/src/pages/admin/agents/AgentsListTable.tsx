import { Bot, Search } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../components/ui/badge"
import { EmptyState } from "../../../components/ui/empty-state"
import { Input } from "../../../components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../../components/ui/table"
import {
  agentEngineLabel,
  agentEngineOf,
  defaultModelOf,
} from "../../../lib/agent-view-model"
import type { Agent, Model } from "../../../lib/api-types"
import { AgentRowActions } from "./AgentRowActions"
import { AgentRuntimeCell } from "./AgentRuntimeCell"
import { AgentStatusBadge } from "./AgentStatusBadge"

export function AgentsListTable({
  agents,
  models,
  keyword,
  chatPendingID,
  deletePending,
  formatRelativeTime,
  onKeywordChange,
  onOpenAgent,
  onChat,
  onEdit,
  onClone,
  onDelete,
}: {
  agents: Agent[]
  models: Model[]
  keyword: string
  chatPendingID: string | null
  deletePending: boolean
  formatRelativeTime: (value: string) => string
  onKeywordChange: (value: string) => void
  onOpenAgent: (agent: Agent) => void
  onChat: (agent: Agent) => void
  onEdit: (agent: Agent) => void
  onClone: (agent: Agent) => void
  onDelete: (agent: Agent) => void
}) {
  const { t } = useTranslation("admin")
  const filtered = agents.filter((agent) => {
    if (!keyword) return true
    const query = keyword.toLowerCase()
    const engine = t(agentEngineLabel(agentEngineOf(agent))).toLowerCase()
    const model = defaultModelOf(agent, models, t("agents.modelUnavailable")).toLowerCase()
    return agent.name.toLowerCase().includes(query)
      || agent.description.toLowerCase().includes(query)
      || agent.slug.toLowerCase().includes(query)
      || engine.includes(query)
      || model.includes(query)
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end gap-3">
        <div className="relative w-72">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
          <Input
            placeholder={t("agents.search.placeholder")}
            className="pl-8 text-xs"
            value={keyword}
            onChange={(event) => onKeywordChange(event.target.value)}
          />
        </div>
      </div>

      {filtered.length === 0 ? (
        <EmptyState
          icon={Bot}
          title={t("agents.emptyFiltered.title")}
          description={t("agents.emptyFiltered.description")}
        />
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("agents.table.agent")}</TableHead>
                <TableHead>{t("agents.table.engine")}</TableHead>
                <TableHead>{t("agents.table.model")}</TableHead>
                <TableHead>{t("agents.table.execution")}</TableHead>
                <TableHead>{t("agents.table.status")}</TableHead>
                <TableHead>{t("agents.table.updated")}</TableHead>
                <TableHead className="text-right pr-4">{t("agents.table.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((agent) => (
                <TableRow
                  key={agent.id}
                  className="cursor-pointer"
                  onClick={() => onOpenAgent(agent)}
                >
                  <TableCell>
                    <div className="flex min-w-0 items-start gap-2">
                      <Bot className="mt-0.5 h-3.5 w-3.5 shrink-0 text-fg-faint" strokeWidth={1.75} />
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-base font-medium text-fg">{agent.name}</span>
                          <Badge variant="neutral">{t(`agents.visibility.${agent.visibility ?? "workspace"}`)}</Badge>
                        </div>
                        <p className="mt-0.5 max-w-md truncate text-sm text-fg-subtle">{agent.description || agent.slug}</p>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="text-sm font-medium text-fg-muted">{t(agentEngineLabel(agentEngineOf(agent)))}</TableCell>
                  <TableCell className="font-mono text-sm text-fg-muted">{defaultModelOf(agent, models, t("agents.modelUnavailable"))}</TableCell>
                  <TableCell><AgentRuntimeCell agent={agent} /></TableCell>
                  <TableCell><AgentStatusBadge status={agent.status} /></TableCell>
                  <TableCell className="whitespace-nowrap text-sm text-fg-subtle">{agent.enabled_at ? formatRelativeTime(agent.enabled_at) : "-"}</TableCell>
                  <TableCell className="pr-4">
                    <AgentRowActions
                      agent={agent}
                      chatPending={chatPendingID === agent.id}
                      deletePending={deletePending}
                      onChat={() => onChat(agent)}
                      onEdit={() => onEdit(agent)}
                      onClone={() => onClone(agent)}
                      onDelete={() => onDelete(agent)}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
