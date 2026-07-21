import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { Inbox, ShieldAlert } from "lucide-react"

import { useAgentInteractions } from "../../lib/api-interactions"
import { InteractionDecisionCard } from "../admin/InteractionDecisionCard"
import { Button } from "../ui/button"
import { Skeleton } from "../ui/skeleton"

export function ConversationInteractionCards({
  workspaceID,
  conversationID,
  preferredRequestID,
  onOpenInbox,
}: {
  workspaceID: string | null
  conversationID: string
  preferredRequestID?: string
  onOpenInbox: () => void
}) {
  const { t } = useTranslation("admin")
  const query = useAgentInteractions(workspaceID, "pending")
  const interactions = useMemo(() => {
    const rows = (query.data?.interactions ?? []).filter(
      (interaction) => interaction.conversation_id === conversationID,
    )
    if (!preferredRequestID) return rows
    return [...rows].sort((left, right) => {
      if (left.request_id === preferredRequestID) return -1
      if (right.request_id === preferredRequestID) return 1
      return 0
    })
  }, [conversationID, preferredRequestID, query.data?.interactions])

  if (!workspaceID) return null
  if (interactions.length === 0 && !preferredRequestID) return null

  return (
    <section aria-label={t("conversations.stream.interactionTitle")} className="space-y-3">
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-warning-border bg-warning-subtle px-4 py-3 text-sm text-warning-emphasis">
        <ShieldAlert className="h-4 w-4 shrink-0" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <p className="font-semibold">{t("conversations.stream.interactionTitle")}</p>
          <p className="text-xs">{t("conversations.stream.interactionDescription")}</p>
        </div>
        <Button size="sm" variant="outline" onClick={onOpenInbox}>
          <Inbox className="h-4 w-4" />
          {t("conversations.stream.viewAllInteractions")}
        </Button>
      </div>

      {interactions.length > 0 ? (
        interactions.map((interaction) => (
          <InteractionDecisionCard
            key={interaction.id}
            interaction={interaction}
            workspaceID={workspaceID}
            className="rounded-xl border border-line bg-surface shadow-sm"
          />
        ))
      ) : query.error ? (
        <div className="rounded-lg border border-danger-border bg-danger-subtle px-4 py-3 text-sm text-danger-emphasis">
          {t("conversations.stream.interactionLoadError")}
        </div>
      ) : (
        <div className="rounded-xl border border-line bg-surface p-5">
          <Skeleton className="h-5 w-48" />
          <Skeleton className="mt-4 h-24 w-full" />
          <p className="mt-3 text-xs text-fg-subtle">
            {t("conversations.stream.loadingInteraction")}
          </p>
        </div>
      )}
    </section>
  )
}
