import { AlertTriangle, Loader2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import { AlertDialog, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "../../components/ui/alert-dialog"
import { Button } from "../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../components/ui/dialog"
import { StatusIcon } from "../../components/ui/status-icon"
import { useAgents } from "../../lib/api-agents"
import type { BulkDeleteModelsResponse } from "../../lib/api-models"

export function ModelDeleteConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  destructive,
  onConfirm,
  onCancel,
  loading,
}: {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
  loading?: boolean
}) {
  const { t } = useTranslation("common")
  return (
    <AlertDialog open={open} onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            {destructive && (
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            )}
            <span>{title}</span>
          </AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <Button variant="outline" onClick={onCancel} disabled={loading}>
            {t("actions.cancel")}
          </Button>
          <Button variant={destructive ? "destructive" : "default"} onClick={onConfirm} disabled={loading}>
            {loading && <Loader2 className="animate-spin" />}
            {confirmLabel ?? t("actions.confirm")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

export interface ModelDeleteResult {
  workspaceID: string | null
  data: BulkDeleteModelsResponse
  names: Record<string, string>
}

export function ModelDeleteResultsDialog({ result, onClose, onCloseAutoFocus }: {
  result: ModelDeleteResult | null
  onClose: () => void
  onCloseAutoFocus: () => void
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const hasReferences = result?.data.failed.some((failure) => failure.references?.length)
  const agentsQ = useAgents(hasReferences ? result?.workspaceID ?? null : null, true)
  const visibleAgentIDs = new Set(agentsQ.data?.agents.map((agent) => agent.id) ?? [])
  if (!result) return null
  const { data, names, workspaceID } = result
  const rows = [
    ...data.failed.map((failure) => ({ id: failure.model_id, failure })),
    ...data.deleted.map((id) => ({ id, failure: undefined })),
  ]

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] max-w-xl overflow-y-auto"
        onCloseAutoFocus={(event) => { event.preventDefault(); onCloseAutoFocus() }}
      >
        <DialogHeader>
          <DialogTitle>{t("models.bulkDelete.resultsTitle")}</DialogTitle>
          <DialogDescription>
            {t("models.bulkDelete.resultSummary", { deleted: data.deleted.length, failed: data.failed.length })}
          </DialogDescription>
        </DialogHeader>
        <ul className="min-w-0 divide-y divide-line">
          {rows.map(({ id, failure }) => (
            <li key={id} className="flex min-w-0 items-start gap-2 py-3 first:pt-0 last:pb-0">
              <StatusIcon status={failure ? "failed" : "completed"} className="mt-0.5 shrink-0" />
              <div className="min-w-0 flex-1 space-y-1 [overflow-wrap:anywhere]">
                <p className="font-medium">{names[id] || id}</p>
                {names[id] && <p className="font-mono text-xs text-fg-muted">{id}</p>}
                <p className="text-fg-muted">
                  {!failure ? t("models.bulkDelete.deleted")
                    : failure.error === "model_in_use" ? t("models.bulkDelete.inUse")
                      : failure.error === "model_not_found" ? t("models.bulkDelete.notFound")
                        : t("models.bulkDelete.failed")}
                </p>
                {failure?.references?.map((agent) => (
                  <div key={agent.id} className="min-w-0">
                    {workspaceID && visibleAgentIDs.has(agent.id) ? (
                      <a
                        className="text-accent underline underline-offset-2"
                        href={`/?${new URLSearchParams({ ws: workspaceID, admin: "agents", id: agent.id, tab: "config" })}`}
                      >
                        {t("models.bulkDelete.openAgent", { name: agent.name || agent.id })}
                      </a>
                    ) : <span>{agent.name || agent.id}</span>}
                    <p className="font-mono text-xs text-fg-muted">{agent.id}</p>
                  </div>
                ))}
                {failure && failure.error !== "model_in_use" && failure.error !== "model_not_found" && (
                  <details className="text-xs text-fg-muted">
                    <summary className="cursor-pointer">{tc("errors.details")}</summary>
                    <p className="mt-1 whitespace-pre-wrap font-mono">{failure.error}</p>
                  </details>
                )}
              </div>
            </li>
          ))}
        </ul>
        <DialogFooter><Button variant="outline" onClick={onClose}>{tc("actions.close")}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
