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
import type { Capability } from "../../../lib/api-types"

interface DeprecateCapabilityDialogProps {
  action: "deprecate" | "undeprecate" | "publish" | "unpublish" | null
  capability: Capability
  installCount: number
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

export function DeprecateCapabilityDialog({ action, capability, installCount, pending, error, onOpenChange, onConfirm }: DeprecateCapabilityDialogProps) {
  const { t } = useTranslation("admin")
  const errMsg = error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null
  if (!action) return null
  const destructive = action === "deprecate" || action === "unpublish"
  return (
    <AlertDialog open={!!action} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(`capabilities.marketStatus.dialog.${action}.title`, { name: capability.name })}</AlertDialogTitle>
          <AlertDialogDescription>{t(`capabilities.marketStatus.dialog.${action}.description`, { count: installCount })}</AlertDialogDescription>
        </AlertDialogHeader>
        {errMsg && <p className="rounded-md bg-red-50 px-3 py-2 text-[12px] text-red-700">{errMsg}</p>}
        <AlertDialogFooter>
          <Button variant="outline" size="sm" disabled={pending} onClick={() => onOpenChange(false)}>{t("capabilities.actions.cancel")}</Button>
          <Button variant={destructive ? "destructive" : "default"} size="sm" disabled={pending} onClick={onConfirm}>{pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t(`capabilities.marketStatus.dialog.${action}.confirm`)}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
