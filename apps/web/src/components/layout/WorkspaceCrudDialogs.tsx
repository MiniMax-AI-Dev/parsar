import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { ApiError } from "../../lib/api-client"
import type { WorkspaceVisibility } from "../../lib/api-types"
import { Button } from "../ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog"
import { Input } from "../ui/input"

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
  const description =
    mode === "create"
      ? t("workspaceCrud.workspace.createDescription")
      : t("workspaceCrud.workspace.renameDescription")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            onSubmit({ name: name.trim(), visibility })
          }}
        >
          <div className="grid gap-1.5">
            <label className="text-sm font-medium text-fg-muted" htmlFor="ws-name">
              {t("workspaceCrud.fields.name")}
            </label>
            <Input
              id="ws-name"
              value={name}
              autoFocus
              required
              onChange={(e) => setName(e.target.value)}
              placeholder={t("workspaceCrud.workspace.namePlaceholder")}
            />
          </div>

          <fieldset className="grid gap-1.5">
            <legend className="text-sm font-medium text-fg-muted">
              {t("workspaceCrud.fields.visibility")}
            </legend>
            <div className="grid grid-cols-2 gap-2">
              {(["private", "public"] as const).map((v) => (
                <label
                  key={v}
                  className={
                    "flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm " +
                    (visibility === v
                      ? "border-line-strong bg-surface-subtle text-fg"
                      : "border-line text-fg-muted hover:bg-surface-subtle")
                  }
                >
                  <input
                    type="radio"
                    name="ws-visibility"
                    value={v}
                    checked={visibility === v}
                    onChange={() => setVisibility(v)}
                    className="h-3 w-3"
                  />
                  {t(`workspaceCrud.visibility.${v}`)}
                </label>
              ))}
            </div>
          </fieldset>

          {errMsg && (
            <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
              {errMsg}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onOpenChange(false)}
            >
              {t("actions.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={pending || !name.trim()}>
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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        {errMsg && (
          <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
            {errMsg}
          </p>
        )}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onOpenChange(false)}
            disabled={pending}
          >
            {t("actions.cancel")}
          </Button>
          <Button
            type="button"
            variant="destructive"
            size="sm"
            onClick={onConfirm}
            disabled={pending}
          >
            {pending ? t("states.loading") : t("workspaceCrud.actions.archive")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t("workspaceCrud.join.title", { name: workspaceName })}
          </DialogTitle>
        </DialogHeader>

        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (tooLong) return
            onSubmit({ reason: trimmed })
          }}
        >
          <div className="grid gap-1.5">
            <label
              className="text-sm font-medium text-fg-muted"
              htmlFor="join-reason"
            >
              {t("workspaceCrud.fields.reason")}
              <span className="ml-1 text-xs font-normal text-fg-faint">
                {t("workspaceCrud.fields.optional")}
              </span>
            </label>
            <textarea
              id="join-reason"
              value={reason}
              autoFocus
              rows={3}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t("workspaceCrud.join.reasonPlaceholder")}
              className="rounded-md border border-line px-3 py-2 text-sm outline-none focus:border-line-strong"
            />
            {tooLong && (
              <p className="text-xs text-danger">
                {t("workspaceCrud.join.reasonTooLong")}
              </p>
            )}
          </div>

          {errMsg && (
            <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
              {errMsg}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onOpenChange(false)}
              disabled={pending}
            >
              {t("actions.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={pending || tooLong}>
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
