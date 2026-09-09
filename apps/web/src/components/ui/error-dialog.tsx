import { AlertTriangle } from "lucide-react"
import type { RefObject } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "./button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "./dialog"
import { VerbatimBlock } from "./verbatim"

export function ErrorDialog({ title, message, detail, onClose, onRestoreFocus, focusScopeRef }: {
  title: string
  message: string
  detail?: string
  onClose: () => void
  onRestoreFocus: () => void
  focusScopeRef: RefObject<HTMLElement | null>
}) {
  const { t } = useTranslation("common")
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        showCloseButton={false}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          onRestoreFocus()
          const scope = focusScopeRef.current
          if (scope && !scope.contains(document.activeElement)) {
            scope.querySelector<HTMLElement>('input:not(:disabled), button:not(:disabled), a[href]')?.focus()
          }
        }}
        className="w-[calc(100%-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto overflow-x-hidden"
      >
        <DialogHeader className="min-w-0 pr-4">
          <DialogTitle className="flex items-start gap-2 [overflow-wrap:anywhere]">
            <AlertTriangle className="h-4 w-4 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            {title}
          </DialogTitle>
        </DialogHeader>
        <DialogDescription className="text-fg [overflow-wrap:anywhere]">{message}</DialogDescription>
        {detail && (
          <details className="min-w-0">
            <summary className="cursor-pointer text-sm text-fg-muted focus-visible:outline-accent">
              {t("errors.details")}
            </summary>
            <VerbatimBlock tabIndex={0} aria-label={t("errors.details")} className="mt-3 max-h-52 w-full break-all focus-visible:outline-accent">{detail}</VerbatimBlock>
          </details>
        )}
        <DialogFooter>
          <Button autoFocus onClick={onClose}>{t("actions.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
