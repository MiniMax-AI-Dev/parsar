import type { KeyboardEvent, ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Badge } from "../../../components/ui/badge"
import { LedgerNum, LedgerRow } from "../../../components/ui/ledger"
import type { Capability } from "../../../lib/api-types"
import { CapabilityTypeBadge } from "./CapabilityTypeBadge"

export function CapabilityRow({
  capability,
  version,
  source,
  availabilityLabel,
  enabledCount,
  credentials,
  age,
  selected,
  onOpen,
  actions,
}: {
  capability: Capability
  version?: string
  source: string
  availabilityLabel: string
  enabledCount: number
  credentials: string
  age: string
  selected: boolean
  onOpen: () => void
  actions: ReactNode
}) {
  const { t } = useTranslation("admin")
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onOpen()
    }
  }
  return (
    <LedgerRow selected={selected} onClick={onOpen} onKeyDown={onKeyDown} className="h-auto min-h-12 py-2">
      <div className="min-w-0 @max-xl/capability-list:col-span-full">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 max-w-full break-words font-medium">{capability.name}</span>
          <CapabilityTypeBadge type={capability.type} />
          {availabilityLabel && <Badge variant="neutral" className="shrink-0" dot>{availabilityLabel}</Badge>}
        </span>
        {capability.description && (
          <span className="block truncate text-xs text-fg-muted" title={capability.description}>{capability.description}</span>
        )}
        <dl className="mt-2 hidden grid-cols-2 gap-x-4 gap-y-1 text-xs @max-xl/capability-list:grid">
          {[
            [t("capabilities.table.version"), version ?? "—"],
            [t("capabilities.marketplaceDetail.source.title"), source],
            [t("capabilities.table.enabledAgents"), String(enabledCount)],
            [t("capabilities.table.credentialsShort"), credentials],
            [t("capabilities.table.updated"), age],
          ].map(([label, value]) => (
            <div key={label} className="grid min-w-0 grid-cols-[4rem_minmax(0,1fr)] gap-x-2">
              <dt className="text-fg-muted">{label}</dt>
              <dd className="break-words">{value}</dd>
            </div>
          ))}
        </dl>
      </div>
      <span className="break-all font-mono text-xs @max-xl/capability-list:hidden">{version ?? "—"}</span>
      <span className="break-words text-xs text-fg-muted @max-xl/capability-list:hidden">{source}</span>
      <LedgerNum className="@max-xl/capability-list:hidden">{enabledCount}</LedgerNum>
      <span className="break-words text-xs text-fg-muted @max-xl/capability-list:hidden">{credentials}</span>
      <span className="break-words text-right text-xs text-fg-muted @max-xl/capability-list:hidden">{age}</span>
      <span className="@max-xl/capability-list:col-start-[-1] @max-xl/capability-list:row-start-1" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        {actions}
      </span>
    </LedgerRow>
  )
}
