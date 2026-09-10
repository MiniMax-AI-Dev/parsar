import type { TFunction } from "i18next"
import type { StatusKind } from "../components/ui/status-icon"
import type { AgentInteraction, AgentInteractionStatus } from "./api-types"
import { firstInteractionQuestion } from "./interaction-questions"

export const INTERACTION_STATUS_ICONS: Record<AgentInteractionStatus, StatusKind> = {
  pending: "queued", resolving: "running", approved: "completed", answered: "completed",
  denied: "failed", cancelled: "cancelled", expired: "interrupted",
}

export function interactionTitle(row: AgentInteraction, t: TFunction<"admin">): string {
  if (row.kind !== "permission") return firstInteractionQuestion(row)?.question || t("approvals.kind.userChoice")
  const raw = String(row.request.resource || row.request.action || t("approvals.kind.permission"))
  return readableInteractionOperation(raw)
}

export function interactionRequester(row: AgentInteraction, t: TFunction<"admin">): string {
  if (row.requested_by_type === "user" && row.requested_by_name) {
    return row.requested_by_id ? `${row.requested_by_name} · ${row.requested_by_id}` : row.requested_by_name
  }
  const type = row.requested_by_type
    ? t(`audit.actor.${row.requested_by_type}`, { defaultValue: row.requested_by_type })
    : t("approvals.detail.requesterUnknown")
  return row.requested_by_id ? `${type} · ${row.requested_by_id}` : type
}

export function readableInteractionOperation(value: string): string {
  return value.replace(/^mcp__(.+?)__(.+)$/, "$1 / $2")
}
