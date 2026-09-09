/* eslint-disable react-refresh/only-export-components */
import { Bot } from "lucide-react"
import { useTranslation } from "react-i18next"
import type { KeyboardEvent } from "react"

import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { InitialTile, Ledger, LedgerHeader, LedgerRow, col } from "../../../components/ui/ledger"
import {
  agentConnectorKey,
  agentConnectorLabel,
  searchAgents,
  agentEngineLabel,
  agentEngineOf,
  defaultModelOf,
} from "../../../lib/agent-view-model"
import { agentActionPermissions } from "../../../lib/agent-actions"
import type { Agent, Model, UserWorkspace } from "../../../lib/api-types"
import { cn } from "../../../lib/utils"
import { AgentRowActions } from "./AgentRowActions"
import { AgentRuntimeCell } from "./AgentRuntimeCell"
import { AgentStatusIcon } from "./AgentStatusBadge"

/** status icon · agent (+ description) · engine · runtime · connector · model · last enabled · actions */
export const AGENTS_LEDGER_COLUMNS = [col.icon(), col.title(0), col.meta(0), col.text(0, 1), col.meta(0), col.id(0, 0.8), col.age(0), col.actions(2)]

export function AgentsListTable({
  agents,
  workspaceRole,
  models,
  keyword,
  connectorFilter,
  onClearFilters,
  selectedID,
  chatPendingID,
  deletePending,
  formatRelativeTime,
  onOpenAgent,
  onChat,
  onEdit,
  onClone,
  onDelete,
}: {
  agents: Agent[]
  workspaceRole?: UserWorkspace["role"]
  models: Model[]
  keyword: string
  connectorFilter: string
  onClearFilters: () => void
  selectedID: string | null
  chatPendingID: string | null
  deletePending: boolean
  formatRelativeTime: (value: string) => string
  onOpenAgent: (agent: Agent) => void
  onChat: (agent: Agent) => void
  onEdit: (agent: Agent) => void
  onClone: (agent: Agent) => void
  onDelete: (agent: Agent) => void
}) {
  const { t } = useTranslation("admin")
  const { canManage, canChat } = agentActionPermissions(workspaceRole)
  const columns = [...AGENTS_LEDGER_COLUMNS.slice(0, -1), ...(canChat ? [col.actions(canManage ? 2 : 1)] : [])]
  const unavailable = t("agents.modelUnavailable")
  const filtered = searchAgents(agents, keyword, models, t).filter(
    (agent) => !connectorFilter || agentConnectorKey(agent.connector_type) === connectorFilter,
  )

  if (filtered.length === 0) {
    return (
      <EmptyState
        icon={Bot}
        title={t("agents.emptyFiltered.title")}
        description={t("agents.emptyFiltered.description")}
        action={
          <Button size="sm" variant="outline" onClick={onClearFilters}>
            {t("agents.emptyFiltered.clear")}
          </Button>
        }
      />
    )
  }

  return (
    <Ledger columns={columns} className="@container/agent-list" role="listbox" aria-label={t("agents.page.title")}>
      <LedgerHeader className="@max-3xl/agent-list:hidden">
        <span />
        <span>{t("agents.table.agent")}</span>
        <span>{t("agents.table.engine")}</span>
        <span>{t("agents.table.runtime")}</span>
        <span>{t("agents.table.connector")}</span>
        <span>{t("agents.table.model")}</span>
        <span className="text-right">{t("agents.table.updated")}</span>
        {canChat && <span />}
      </LedgerHeader>
      <ul className="m-0 list-none p-0">
        {filtered.map((agent) => {
          const model = defaultModelOf(agent, models, unavailable)
          const fields = [
            { label: t("agents.table.engine"), value: t(agentEngineLabel(agentEngineOf(agent))) },
            { label: t("agents.table.runtime"), value: <AgentRuntimeCell agent={agent} /> },
            { label: t("agents.table.connector"), value: agentConnectorLabel(agent.connector_type), className: "text-xs text-fg-muted" },
            { label: t("agents.table.model"), value: <span className={cn("font-mono text-xs", model === "—" && "text-fg-muted")}>{model}</span> },
            { label: t("agents.table.updated"), value: agent.enabled_at ? formatRelativeTime(agent.enabled_at) : "—", className: "text-xs text-fg-muted" },
          ]
          const onKeyDown = (event: KeyboardEvent<HTMLLIElement>) => {
            if (event.target !== event.currentTarget) return
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault()
              onOpenAgent(agent)
            }
          }
          return (
            <LedgerRow key={agent.id} selected={agent.id === selectedID} onClick={() => onOpenAgent(agent)} onKeyDown={onKeyDown} className="@max-3xl/agent-list:h-auto @max-3xl/agent-list:py-2">
              <span className="@max-3xl/agent-list:hidden"><AgentStatusIcon status={agent.status} /></span>
              <div className="min-w-0 @max-3xl/agent-list:col-span-full">
                <div className={cn("flex min-w-0 items-center gap-1.5 @max-3xl/agent-list:min-h-7 @max-3xl/agent-list:items-start", canChat && (canManage ? "@max-3xl/agent-list:pr-20" : "@max-3xl/agent-list:pr-12"))}>
                  <span className="mt-0.5 hidden @max-3xl/agent-list:block"><AgentStatusIcon status={agent.status} /></span>
                  <InitialTile name={agent.name} />
                  <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden @max-3xl/agent-list:flex-col @max-3xl/agent-list:items-start @max-3xl/agent-list:gap-1">
                    <span className="min-w-0 max-w-full shrink-0 truncate font-medium @max-3xl/agent-list:whitespace-normal @max-3xl/agent-list:break-words" title={agent.name}>{agent.name}</span>
                    {agent.description && <span className="min-w-0 max-w-full truncate text-xs text-fg-muted" title={agent.description}>{agent.description}</span>}
                  </div>
                </div>
                <dl className="mt-2 hidden grid-cols-2 gap-x-4 gap-y-1 text-xs @max-3xl/agent-list:grid @max-sm/agent-list:grid-cols-1">
                  {fields.map(({ label, value }) => (
                    <div key={label} className="grid min-w-0 grid-cols-[4rem_minmax(0,1fr)] gap-x-2">
                      <dt className="text-fg-muted">{label}</dt>
                      <dd className="min-w-0 break-words">{value}</dd>
                    </div>
                  ))}
                </dl>
              </div>
              {fields.map(({ label, value, className }) => <span key={label} className={cn("truncate @max-3xl/agent-list:hidden", className)}>{value}</span>)}
              {canChat && <span className="@max-3xl/agent-list:absolute @max-3xl/agent-list:right-0 @max-3xl/agent-list:top-2 @max-3xl/agent-list:h-7 @max-3xl/agent-list:w-0" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
                <AgentRowActions
                  agent={agent}
                  canManage={canManage}
                  chatPending={chatPendingID === agent.id}
                  deletePending={deletePending}
                  onChat={() => onChat(agent)}
                  onEdit={() => onEdit(agent)}
                  onClone={() => onClone(agent)}
                  onDelete={() => onDelete(agent)}
                />
              </span>}
            </LedgerRow>
          )
        })}
      </ul>
    </Ledger>
  )
}
