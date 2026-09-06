/* eslint-disable react-refresh/only-export-components */
import { Bot } from "lucide-react"
import { useTranslation } from "react-i18next"
import type { KeyboardEvent } from "react"

import { EmptyState } from "../../../components/ui/empty-state"
import { InitialTile, Ledger, LedgerHeader, LedgerRow, col } from "../../../components/ui/ledger"
import {
  agentConnectorLabel,
  agentEngineLabel,
  agentEngineOf,
  defaultModelOf,
} from "../../../lib/agent-view-model"
import type { Agent, Model } from "../../../lib/api-types"
import { cn } from "../../../lib/utils"
import { AgentRowActions } from "./AgentRowActions"
import { AgentRuntimeCell } from "./AgentRuntimeCell"
import { AgentStatusIcon } from "./AgentStatusBadge"

/** status icon · agent (+ description) · engine · runtime · connector · model · last enabled · actions */
export const AGENTS_LEDGER_COLUMNS = [col.icon(), col.title(), col.meta(104), col.text(200, 1), col.meta(104), col.id(168, 0.8), col.age(80), col.actions(2)]

export function AgentsListTable({
  agents,
  models,
  keyword,
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
  models: Model[]
  keyword: string
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
  const unavailable = t("agents.modelUnavailable")
  const filtered = agents.filter((agent) => {
    if (!keyword) return true
    const query = keyword.toLowerCase()
    const engine = t(agentEngineLabel(agentEngineOf(agent))).toLowerCase()
    const model = defaultModelOf(agent, models, unavailable).toLowerCase()
    return agent.name.toLowerCase().includes(query)
      || agent.description.toLowerCase().includes(query)
      || agent.slug.toLowerCase().includes(query)
      || engine.includes(query)
      || model.includes(query)
  })

  if (filtered.length === 0) {
    return (
      <EmptyState
        icon={Bot}
        title={t("agents.emptyFiltered.title")}
        description={t("agents.emptyFiltered.description")}
      />
    )
  }

  return (
    <Ledger columns={AGENTS_LEDGER_COLUMNS} role="listbox" aria-label={t("agents.page.title")}>
      <LedgerHeader>
        <span />
        <span>{t("agents.table.agent")}</span>
        <span>{t("agents.table.engine")}</span>
        <span>{t("agents.table.runtime")}</span>
        <span>{t("agents.table.connector")}</span>
        <span>{t("agents.table.model")}</span>
        <span className="text-right">{t("agents.table.updated")}</span>
        <span />
      </LedgerHeader>
      <ul className="m-0 list-none p-0">
        {filtered.map((agent) => {
          const model = defaultModelOf(agent, models, unavailable)
          const onKeyDown = (event: KeyboardEvent<HTMLLIElement>) => {
            if (event.target !== event.currentTarget) return
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault()
              onOpenAgent(agent)
            }
          }
          return (
            <LedgerRow key={agent.id} onClick={() => onOpenAgent(agent)} onKeyDown={onKeyDown}>
              <AgentStatusIcon status={agent.status} />
              <span className="flex min-w-0 items-center gap-1.5">
                <InitialTile name={agent.name} />
                <span className="shrink-0 truncate font-medium">{agent.name}</span>
                {agent.description && (
                  <span className="min-w-0 truncate text-xs text-fg-muted">· {agent.description}</span>
                )}
              </span>
              <span className="truncate">{t(agentEngineLabel(agentEngineOf(agent)))}</span>
              <AgentRuntimeCell agent={agent} />
              <span className="truncate text-xs text-fg-muted">{agentConnectorLabel(agent.connector_type)}</span>
              <span className={cn("truncate font-mono text-xs", model === "—" ? "text-fg-muted" : "text-fg")}>{model}</span>
              <span className="truncate text-right text-xs text-fg-muted">
                {agent.enabled_at ? formatRelativeTime(agent.enabled_at) : "—"}
              </span>
              <AgentRowActions
                agent={agent}
                chatPending={chatPendingID === agent.id}
                deletePending={deletePending}
                onChat={() => onChat(agent)}
                onEdit={() => onEdit(agent)}
                onClone={() => onClone(agent)}
                onDelete={() => onDelete(agent)}
              />
            </LedgerRow>
          )
        })}
      </ul>
    </Ledger>
  )
}
