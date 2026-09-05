import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle } from "lucide-react"

import { useAgentInteractions } from "../../lib/api-interactions"
import { InteractionDecisionCard } from "../admin/InteractionDecisionCard"
import { Skeleton } from "../ui/skeleton"

/**
 * Pending approval / user-input requests for this conversation, rendered
 * in the thread as a hairline-headed section. The Inbox itself is one
 * click away in the sidebar, so there is no second link to it here.
 */
export function ConversationInteractionCards({
  workspaceID,
  conversationID,
  preferredRequestID,
}: {
  workspaceID: string | null
  conversationID: string
  preferredRequestID?: string
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

  const title = t("conversations.stream.interactionTitle")

  return (
    <section aria-label={title}>
      <h2 className="flex h-8 items-center justify-between border-b border-line text-sm font-medium text-fg">
        <span>{title}</span>
        {interactions.length > 1 && (
          <span className="text-xs font-normal tabular-nums text-fg-muted">{interactions.length}</span>
        )}
      </h2>

      {interactions.length > 0 ? (
        interactions.map((interaction) => (
          <InteractionDecisionCard
            key={interaction.id}
            interaction={interaction}
            workspaceID={workspaceID}
            hideConversationLink
            className="border-b border-line py-4 last:border-b-0"
          />
        ))
      ) : query.error ? (
        <p className="flex items-start gap-1.5 py-3 text-sm text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <span>{t("conversations.stream.interactionLoadError")}</span>
        </p>
      ) : (
        <div className="space-y-2 py-4" aria-busy="true" aria-label={t("conversations.stream.loadingInteraction")}>
          <Skeleton className="h-3 w-48" />
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-2/3" />
        </div>
      )}
    </section>
  )
}
