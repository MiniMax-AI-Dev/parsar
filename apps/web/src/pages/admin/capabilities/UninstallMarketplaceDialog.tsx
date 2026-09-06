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
import { InitialTile } from "../../../components/ui/ledger"
import { ApiError } from "../../../lib/api-client"
import type { EnabledMarketplaceAgent, TargetMarketplaceInstall } from "../../../lib/api-marketplace"
import { InlineNotice } from "./notices"

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
  const rows = agents.length > 0 ? agents : [{ name: t("capabilities.uninstall.unknownAgent") }]
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("capabilities.uninstall.title", { name: capability.name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("capabilities.uninstall.description", { count })}</AlertDialogDescription>
        </AlertDialogHeader>
        <div className="space-y-3">
          <ul className="m-0 max-h-44 list-none overflow-auto border-t border-line p-0">
            {rows.map((agent, index) => {
              const name = agent.name ?? agent.agent_name ?? t("capabilities.uninstall.unknownAgent")
              return (
                <li key={agent.agent_id ?? agent.id ?? index} className="flex h-8 items-center gap-2 border-b border-line text-sm text-fg">
                  <InitialTile name={name} />
                  <span className="truncate">{name}</span>
                </li>
              )
            })}
          </ul>
          <p className="text-xs text-fg-muted">{t("capabilities.uninstall.credentialNote")}</p>
          {errMsg && <InlineNotice tone="error">{errMsg}</InlineNotice>}
        </div>
        <AlertDialogFooter>
          <Button variant="outline" disabled={pending} onClick={() => onOpenChange(false)}>{t("capabilities.actions.cancel")}</Button>
          <Button variant="destructive" disabled={pending} onClick={onConfirm}>
            {pending && <Loader2 className="animate-spin" />}
            {t("capabilities.uninstall.confirm", { count })}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
