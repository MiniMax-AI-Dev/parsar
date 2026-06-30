import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../../components/ui/alert-dialog"
import { Button } from "../../../components/ui/button"
import { ApiError } from "../../../lib/api-client"
import type { EnabledMarketplaceAgent, TargetMarketplaceInstall } from "../../../lib/api-marketplace"

interface UninstallMarketplaceDialogProps {
  capability: TargetMarketplaceInstall
  agents: EnabledMarketplaceAgent[]
  open: boolean
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

export function UninstallMarketplaceDialog({ capability, agents, open, pending, error, onOpenChange, onConfirm }: UninstallMarketplaceDialogProps) {
  const { t } = useTranslation("admin")
  const errMsg = error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null
  const count = agents.length || capability.enabled_agent_count || 0
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("capabilities.uninstall.title", { name: capability.name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("capabilities.uninstall.description", { count })}</AlertDialogDescription>
        </AlertDialogHeader>
        <div className="space-y-3">
          <ul className="max-h-44 space-y-1 overflow-auto rounded-md border border-line bg-surface-subtle px-3 py-2 text-sm text-fg-muted">
            {(agents.length > 0 ? agents : [{ name: t("capabilities.uninstall.unknownAgent") }]).map((agent, index) => (
              <li key={agent.agent_id ?? agent.id ?? index}>· {agent.name ?? agent.agent_name ?? t("capabilities.uninstall.unknownAgent")}</li>
            ))}
          </ul>
          <div className="rounded-md border border-warning-border bg-warning-subtle px-3 py-2 text-sm text-warning-emphasis">
            {t("capabilities.uninstall.credentialNote")}
          </div>
          {errMsg && <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">{errMsg}</p>}
        </div>
        <AlertDialogFooter>
          <Button variant="outline" size="sm" disabled={pending} onClick={() => onOpenChange(false)}>{t("capabilities.actions.cancel")}</Button>
          <Button variant="destructive" size="sm" disabled={pending} onClick={onConfirm}>{pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("capabilities.uninstall.confirm", { count })}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
