import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../../components/ui/alert-dialog"
import { Button } from "../../../components/ui/button"
import { InlineError } from "../../../components/runtime/InlineError"

/** Type-to-nothing confirm for removing a paired runtime. */
export function ConfirmDeleteRuntimeDialog({
  targetName,
  pending,
  error,
  onCancel,
  onConfirm,
}: {
  targetName: string
  pending: boolean
  error?: Error
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  return (
    <AlertDialog open onOpenChange={(next) => { if (!next && !pending) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t("runtime.agentDaemon.delete.title", { name: targetName, defaultValue: "Delete device {{name}}" })}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t("runtime.agentDaemon.delete.description", {
              defaultValue: "Once deleted, this device can no longer accept new tasks; running tasks are unaffected. This action cannot be undone.",
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && <InlineError>{error.message}</InlineError>}
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button variant="outline" disabled={pending}>
              {tc("actions.cancel")}
            </Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button
              variant="destructive"
              onClick={(e) => { e.preventDefault(); onConfirm() }}
              disabled={pending}
            >
              {pending && <Loader2 className="animate-spin" />}
              {t("runtime.agentDaemon.delete.confirm", { defaultValue: "Delete" })}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
