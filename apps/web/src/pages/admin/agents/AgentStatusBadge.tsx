import { useTranslation } from "react-i18next"

import { Badge } from "../../../components/ui/badge"
import { StatusIcon, type StatusKind } from "../../../components/ui/status-icon"
import type { Agent } from "../../../lib/api-types"

const STATUS_KIND: Record<Agent["status"], StatusKind> = {
  active: "completed",
  disabled: "cancelled",
  error: "failed",
}

/** Agent state as a chip: the word in ink, the state in the 6px dot. */
export function AgentStatusBadge({ status }: { status: Agent["status"] }) {
  const { t } = useTranslation("admin")
  const variant = status === "active" ? "success" : status === "error" ? "destructive" : "neutral"
  return (
    <Badge variant={variant} dot>
      {t(`agents.status.${status === "active" ? "active" : status === "error" ? "error" : "disabled"}`)}
    </Badge>
  )
}

/** Agent state as the 14px status icon (ledger rows). */
export function AgentStatusIcon({ status, className }: { status: Agent["status"]; className?: string }) {
  const { t } = useTranslation("admin")
  const key = status === "active" ? "active" : status === "error" ? "error" : "disabled"
  return <StatusIcon status={STATUS_KIND[status] ?? "cancelled"} title={t(`agents.status.${key}`)} className={className} />
}
