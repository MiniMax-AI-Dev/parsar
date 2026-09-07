import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { ApiError } from "../../lib/api-client"
import type { WorkspaceVisibility } from "../../lib/api-types"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../ui/alert-dialog"
import { Button } from "../ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog"
import { InlineError } from "../ui/error-state"
import { Input } from "../ui/input"
import { Field } from "../ui/label"
import { Select } from "../ui/select"
import { Textarea } from "../ui/textarea"

type FormMode = "create" | "rename"

interface WorkspaceFormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  mode: FormMode
  initialName?: string
  /** Used to prefill in Rename mode (defaults to "private" on create). */
  initialVisibility?: WorkspaceVisibility
  pending: boolean
  error: unknown
  onSubmit: (values: {
    name: string
    visibility: WorkspaceVisibility
  }) => void
}

function extractErrorMessage(err: unknown): string | null {
  if (!err) return null
  if (err instanceof ApiError) {
    return err.envelope.message || err.message
  }
  if (err instanceof Error) return err.message
  return String(err)
}

export function WorkspaceFormDialog({
  open,
  onOpenChange,
  mode,
  initialName = "",
  initialVisibility = "private",
  pending,
  error,
  onSubmit,
}: WorkspaceFormDialogProps) {
  const { t } = useTranslation("common")
  const [name, setName] = useState(initialName)
  const [visibility, setVisibility] =
    useState<WorkspaceVisibility>(initialVisibility)

  // Reset on open so a previous error doesn't leak across opens.
  useEffect(() => {
    if (open) {
      setName(initialName)
      setVisibility(initialVisibility)
    }
  }, [open, initialName, initialVisibility])

  const errMsg = extractErrorMessage(error)
  const submitLabel =
    mode === "create"
      ? t("workspaceCrud.actions.create")
      : t("workspaceCrud.actions.save")
  const title =
    mode === "create"
      ? t("workspaceCrud.workspace.createTitle")
      : t("workspaceCrud.workspace.renameTitle")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>

        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            onSubmit({ name: name.trim(), visibility })
          }}
        >
          <Field label={t("workspaceCrud.fields.name")} htmlFor="ws-name">
            <Input
              id="ws-name"
              value={name}
              autoFocus
              required
              onChange={(e) => setName(e.target.value)}
              placeholder={t("workspaceCrud.workspace.namePlaceholder")}
            />
          </Field>

          <Field label={t("workspaceCrud.fields.visibility")} htmlFor="ws-visibility">
            <Select
              id="ws-visibility"
              value={visibility}
              onChange={(e) => setVisibility(e.target.value as WorkspaceVisibility)}
            >
              {(["private", "public"] as const).map((v) => (
                <option key={v} value={v}>
                  {t(`workspaceCrud.visibility.${v}`)}
                </option>
              ))}
            </Select>
          </Field>

          <DialogFooter className="mt-1">
            {errMsg && <InlineError className="mr-auto">{errMsg}</InlineError>}
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("actions.cancel")}
            </Button>
            <Button type="submit" disabled={pending || !name.trim()}>
              {pending ? t("states.loading") : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

interface ConfirmArchiveDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  pending: boolean
  error: unknown
  onConfirm: () => void
}

export function ConfirmArchiveDialog({
  open,
  onOpenChange,
  title,
  description,
  pending,
  error,
  onConfirm,
}: ConfirmArchiveDialogProps) {
  const { t } = useTranslation("common")
  const errMsg = extractErrorMessage(error)
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>

        <AlertDialogFooter>
          {errMsg && <InlineError className="mr-auto">{errMsg}</InlineError>}
          <AlertDialogCancel asChild>
            <Button type="button" variant="outline" disabled={pending}>
              {t("actions.cancel")}
            </Button>
          </AlertDialogCancel>
          <Button type="button" variant="destructive" onClick={onConfirm} disabled={pending}>
            {pending ? t("states.loading") : t("workspaceCrud.actions.archive")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

interface JoinRequestDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceName: string
  pending: boolean
  error: unknown
  onSubmit: (values: { reason: string }) => void
}

export function JoinRequestDialog({
  open,
  onOpenChange,
  workspaceName,
  pending,
  error,
  onSubmit,
}: JoinRequestDialogProps) {
  const { t } = useTranslation("common")
  const [reason, setReason] = useState("")

  // Reset on open so prior input doesn't leak to a different workspace.
  useEffect(() => {
    if (open) setReason("")
  }, [open])

  const errMsg = extractErrorMessage(error)
  const trimmed = reason.trim()
  const tooLong = trimmed.length > 1000

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>
            {t("workspaceCrud.join.title", { name: workspaceName })}
          </DialogTitle>
        </DialogHeader>

        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (tooLong) return
            onSubmit({ reason: trimmed })
          }}
        >
          <Field
            label={`${t("workspaceCrud.fields.reason")} ${t("workspaceCrud.fields.optional")}`}
            htmlFor="join-reason"
            hint={tooLong ? <InlineError>{t("workspaceCrud.join.reasonTooLong")}</InlineError> : undefined}
          >
            <Textarea
              id="join-reason"
              value={reason}
              autoFocus
              rows={3}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t("workspaceCrud.join.reasonPlaceholder")}
            />
          </Field>

          <DialogFooter className="mt-1">
            {errMsg && <InlineError className="mr-auto">{errMsg}</InlineError>}
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
              {t("actions.cancel")}
            </Button>
            <Button type="submit" disabled={pending || tooLong}>
              {pending
                ? t("states.loading")
                : t("workspaceCrud.actions.submitJoinRequest")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
