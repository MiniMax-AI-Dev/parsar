import { History } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Skeleton } from "../../../components/ui/skeleton"
import { StatusIcon } from "../../../components/ui/status-icon"
import { useNavigateAdmin } from "../../../lib/admin-router"
import { useScheduledTaskRuns, type ScheduledTask } from "../../../lib/api-scheduled-tasks"
import { formatScheduledTaskTime, scheduledTaskStatusIcon } from "../../../lib/scheduled-task-format"

export function ScheduledTaskHistoryDialog({ task, onClose, onCloseAutoFocus }: {
  task: ScheduledTask
  onClose: () => void
  onCloseAutoFocus: (event: Event) => void
}) {
  const { t } = useTranslation("admin")
  const navigate = useNavigateAdmin()
  const runsQ = useScheduledTaskRuns(task.id)
  const runs = runsQ.data ?? []

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="max-h-[calc(100vh-2rem)] w-[calc(100%-2rem)] max-w-lg overflow-x-hidden overflow-y-auto" onCloseAutoFocus={onCloseAutoFocus}>
        <DialogHeader>
          <DialogTitle>{t("scheduledTasks.history.title")}</DialogTitle>
          <DialogDescription className="break-words">{t("scheduledTasks.history.description", { name: task.name })}</DialogDescription>
        </DialogHeader>
        {runsQ.isLoading ? (
          <div className="space-y-3" aria-busy="true">
            {Array.from({ length: 3 }, (_, index) => <Skeleton key={index} className="h-14 w-full" />)}
          </div>
        ) : runsQ.error ? (
          <ErrorState title={t("scheduledTasks.history.loadError")} detail={runsQ.error instanceof Error ? runsQ.error.message : undefined} onRetry={() => void runsQ.refetch()} />
        ) : runs.length === 0 ? (
          <EmptyState icon={History} size="compact" title={t("scheduledTasks.history.empty")} description={t("scheduledTasks.history.emptyDescription")} />
        ) : (
          <ul className="m-0 list-none divide-y divide-line p-0">
            {runs.map((run) => (
              <li key={run.id} className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 py-3 first:pt-0 last:pb-0">
                <div className="min-w-0 space-y-1">
                  <span className="flex items-center gap-1.5 font-medium">
                    <StatusIcon status={scheduledTaskStatusIcon(run.status)} />
                    {t(`scheduledTasks.status.${run.status}` as never, { defaultValue: run.status })}
                  </span>
                  <time dateTime={run.created_at} className="font-mono text-xs text-fg-muted">{formatScheduledTaskTime(run.created_at)}</time>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button variant="outline" size="sm" onClick={() => navigate("runs", { id: run.id })}>{t("scheduledTasks.history.openRun")}</Button>
                  {run.conversation_id && (
                    <Button variant="outline" size="sm" onClick={() => navigate("conversations", { id: run.conversation_id })}>{t("scheduledTasks.history.openConversation")}</Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </DialogContent>
    </Dialog>
  )
}
