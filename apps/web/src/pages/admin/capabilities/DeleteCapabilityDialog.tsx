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

interface DeleteCapabilityDialogProps {
  capability: Capability | null
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

// Delete = release the capability.name slot; the server writes deleted_at.
// If any agent is still bound the server returns 409 with envelope
// { code: "capability_in_use", message: "<localized>", binding_count: N }.
// envelope.message is already a display-ready string, so we render it as-is.
// Same shape as DeprecateCapabilityDialog, but a separate dialog because the
// semantics differ: delete is one-shot, with no toggle / install count.
export function DeleteCapabilityDialog({ capability, pending, error, onOpenChange, onConfirm }: DeleteCapabilityDialogProps) {
  const { t } = useTranslation("admin")
  const errMsg = error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null
  if (!capability) return null
  return (
    <AlertDialog open={!!capability} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("capabilities.delete.dialog.title", { name: capability.name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("capabilities.delete.dialog.description")}</AlertDialogDescription>
        </AlertDialogHeader>
        {errMsg && <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">{errMsg}</p>}
        <AlertDialogFooter>
          <Button variant="outline" size="sm" disabled={pending} onClick={() => onOpenChange(false)}>{t("capabilities.actions.cancel")}</Button>
          <Button variant="destructive" size="sm" disabled={pending} onClick={onConfirm}>{pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("capabilities.delete.dialog.confirm")}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
