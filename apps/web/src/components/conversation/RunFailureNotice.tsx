import { useRef } from "react"
import { useTranslation } from "react-i18next"
import { useRetryRun } from "../../lib/api-agents"
import type { ConversationTimelineRun } from "../../lib/api-types"
import { Button } from "../ui/button"
import { ErrorDialog } from "../ui/error-dialog"
import { ErrorState } from "../ui/error-state"

export function RunFailureNotice({
  run,
  workspaceID,
  canRetry,
  onRunStarted,
}: {
  run: ConversationTimelineRun
  workspaceID: string | null
  canRetry: boolean
  onRunStarted: (runID: string) => void
}) {
  const { t } = useTranslation("admin")
  const retry = useRetryRun(workspaceID)
  const scopeRef = useRef<HTMLDivElement>(null)
  const retryRef = useRef<HTMLButtonElement>(null)
  return (
    <div ref={scopeRef}>
      <ErrorState
        appearance="panel"
        announce={false}
        title={t("conversations.runFailure.title")}
        action={workspaceID ? (
          <div className="flex flex-wrap items-center gap-2">
            <Button asChild variant="outline" size="sm">
              <a href={`/?${new URLSearchParams({ ws: workspaceID, admin: "runs", id: run.id })}`}>
                {t("conversations.detail.viewRunLink")}
              </a>
            </Button>
            {canRetry && !run.agent_deleted && <Button
              ref={retryRef}
              variant="outline"
              size="sm"
              disabled={retry.isPending}
              onClick={() => retry.mutate(
                { runID: run.id, reason: "user_clicked_retry" },
                { onSuccess: (result) => onRunStarted(result.run_id) },
              )}
            >
              {t("runs.actions.retry")}
            </Button>}
          </div>
        ) : undefined}
      />
      {retry.error && <ErrorDialog
        title={t("conversations.runFailure.retryErrorTitle")}
        message={t("conversations.runFailure.retryError")}
        detail={retry.error.message}
        onClose={() => retry.reset()}
        onRestoreFocus={() => retryRef.current?.focus()}
        focusScopeRef={scopeRef}
      />}
    </div>
  )
}
