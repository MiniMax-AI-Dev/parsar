import { useTranslation } from "react-i18next"
import type { AgentInteraction, AgentInteractionStatus } from "../../lib/api-types"
import { interactionRequester, interactionTitle, INTERACTION_STATUS_ICONS } from "../../lib/interaction-presentation"
import { useRelativeTime } from "../../lib/relative-time"
import { Ledger, LedgerGroup, LedgerHeader, LedgerRow, col } from "../ui/ledger"
import { Property, PropertyList } from "../ui/property-list"
import { StatusIcon } from "../ui/status-icon"

const GROUP_ORDER: AgentInteractionStatus[] = ["pending", "resolving", "approved", "answered", "denied", "cancelled", "expired"]
const COLUMNS = [col.icon(), col.title(0), col.text(0), col.text(0), col.age(80)]

export function InteractionList({ rows, selectedID, label, onSelect }: {
  rows: AgentInteraction[]
  selectedID?: string
  label: string
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  return (
    <Ledger columns={COLUMNS} className="@container/interactions" role="listbox" aria-label={label}>
      <LedgerHeader className="@max-2xl/interactions:hidden">
        <span />
        <span>{t("approvals.table.request")}</span>
        <span>{t("approvals.detail.requester")}</span>
        <span>{t("approvals.detail.agent")}</span>
        <span>{t("runs.table.age")}</span>
      </LedgerHeader>
      {GROUP_ORDER.map((status) => {
        const group = rows.filter((row) => row.status === status)
        if (!group.length) return null
        return (
          <LedgerGroup key={status} label={t(`approvals.status.${status}`)} count={group.length}>
            {group.map((row) => {
              const title = interactionTitle(row, t)
              const requester = interactionRequester(row, t)
              const fields = [
                { label: t("approvals.detail.requester"), value: requester },
                { label: t("approvals.detail.agent"), value: row.agent_name || "—" },
                { label: t("runs.table.age"), value: fmtAgo(row.created_at) },
              ]
              return (
                <LedgerRow key={row.id} selected={row.id === selectedID} onClick={() => onSelect(row.id)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onSelect(row.id) }
                  }}
                  className="h-auto min-h-12 py-2"
                >
                  <span className="@max-2xl/interactions:hidden"><StatusIcon status={INTERACTION_STATUS_ICONS[row.status]} title={t(`approvals.status.${row.status}`)} /></span>
                  <div className="min-w-0 @max-2xl/interactions:col-span-full">
                    <div className="flex min-w-0 items-start gap-1.5">
                      <span className="hidden @max-2xl/interactions:block"><StatusIcon status={INTERACTION_STATUS_ICONS[row.status]} title={t(`approvals.status.${row.status}`)} /></span>
                      <span className="min-w-0 break-words font-medium" title={title}>{title}</span>
                    </div>
                    <div className="mt-1 truncate text-xs text-fg-muted" title={row.conversation_title || row.conversation_id}>
                      {t("approvals.detail.conversation")}: {row.conversation_title || row.conversation_id}
                    </div>
                    <PropertyList className="mt-2 hidden @max-2xl/interactions:grid">
                      {fields.map((field) => <Property key={field.label} label={field.label} className="h-auto min-h-7 items-start whitespace-normal py-1">
                        <span className="break-words">{field.value}</span>
                      </Property>)}
                    </PropertyList>
                  </div>
                  {fields.map((field) => <span key={field.label} className="truncate text-xs @max-2xl/interactions:hidden" title={field.value}>{field.value}</span>)}
                </LedgerRow>
              )
            })}
          </LedgerGroup>
        )
      })}
    </Ledger>
  )
}
