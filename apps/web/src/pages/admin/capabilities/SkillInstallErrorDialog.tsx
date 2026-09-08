import { useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, Copy } from "lucide-react"

import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { VerbatimBlock } from "../../../components/ui/verbatim"
import { copyText } from "../../../lib/clipboard"

export function SkillInstallErrorDialog({ name, detail, onClose, onRetry }: {
  name: string
  detail: string
  onClose: () => void
  onRetry: () => void
}) {
  const { t } = useTranslation("admin")
  const { t: common } = useTranslation("common")
  const [copyStatus, setCopyStatus] = useState<"idle" | "copied" | "copyFailed">("idle")
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent showCloseButton={false} className="w-[calc(100%-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto overflow-x-hidden">
        <DialogHeader className="min-w-0 pr-4">
          <DialogTitle className="flex items-start gap-2">
            <AlertTriangle className="h-4 w-4 shrink-0 text-status-failed" aria-hidden="true" />
            {t("capabilities.skillsDirectory.install.failed")}
          </DialogTitle>
          <DialogDescription className="break-words">
            {t("capabilities.skillsDirectory.install.failureDescription", { name })}
          </DialogDescription>
        </DialogHeader>
        <details className="min-w-0">
          <summary className="cursor-pointer text-sm text-fg focus-visible:outline-accent">
            {t("capabilities.skillsDirectory.install.details")}
          </summary>
          <VerbatimBlock className="mt-3 max-h-52 w-full">{detail}</VerbatimBlock>
          <Button variant="outline" size="sm" className="mt-2" onClick={async () => setCopyStatus(await copyText(detail) ? "copied" : "copyFailed")}>
            <Copy className="h-3.5 w-3.5" aria-hidden="true" />
            {t("capabilities.skillsDirectory.install.copy")}
          </Button>
          {copyStatus !== "idle" && <p role="status" className="mt-2 text-xs text-fg-muted">{t(`capabilities.skillsDirectory.install.${copyStatus}`)}</p>}
        </details>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{common("actions.close")}</Button>
          <Button onClick={onRetry}>{common("actions.retry")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
