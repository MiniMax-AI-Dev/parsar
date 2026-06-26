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
  /** Rename 模式下用于回填(create 时默认 "private")。 */
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
            <label className="text-[12px] font-medium text-slate-700" htmlFor="ws-name">
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
            <legend className="text-[12px] font-medium text-slate-700">
              {t("workspaceCrud.fields.visibility")}
            </legend>
            <div className="grid grid-cols-2 gap-2">
              {(["private", "public"] as const).map((v) => (
                <label
                  key={v}
                  className={
                    "flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-[12px] " +
                    (visibility === v
                      ? "border-slate-700 bg-slate-50 text-slate-900"
                      : "border-slate-200 text-slate-700 hover:bg-slate-50")
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
            <p className="rounded-md bg-red-50 px-3 py-2 text-[12px] text-red-700">
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

interface ProjectFormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  mode: FormMode
  initialName?: string
  initialDescription?: string
  pending: boolean
  error: unknown
  onSubmit: (values: { name: string; description: string }) => void
}

export function ProjectFormDialog({
  open,
  onOpenChange,
  mode,
  initialName = "",
  initialDescription = "",
  pending,
  error,
  onSubmit,
}: ProjectFormDialogProps) {
  const { t } = useTranslation("common")
  const [name, setName] = useState(initialName)
  const [description, setDescription] = useState(initialDescription)

  useEffect(() => {
    if (open) {
      setName(initialName)
      setDescription(initialDescription)
    }
  }, [open, initialName, initialDescription])

  const errMsg = extractErrorMessage(error)
  const submitLabel =
    mode === "create"
      ? t("workspaceCrud.actions.create")
      : t("workspaceCrud.actions.save")
  const title =
    mode === "create"
      ? t("workspaceCrud.project.createTitle")
      : t("workspaceCrud.project.renameTitle")
  const description1 =
    mode === "create"
      ? t("workspaceCrud.project.createDescription")
      : t("workspaceCrud.project.renameDescription")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description1}</DialogDescription>
        </DialogHeader>

        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            onSubmit({
              name: name.trim(),
              description: description.trim(),
            })
          }}
        >
          <div className="grid gap-1.5">
            <label
              className="text-[12px] font-medium text-slate-700"
              htmlFor="proj-name"
            >
              {t("workspaceCrud.fields.name")}
            </label>
            <Input
              id="proj-name"
              value={name}
              autoFocus
              required
              onChange={(e) => setName(e.target.value)}
              placeholder={t("workspaceCrud.project.namePlaceholder")}
            />
          </div>

          <div className="grid gap-1.5">
            <label
              className="text-[12px] font-medium text-slate-700"
              htmlFor="proj-desc"
            >
              {t("workspaceCrud.fields.description")}
              <span className="ml-1 text-[11px] font-normal text-slate-400">
                {t("workspaceCrud.fields.optional")}
              </span>
            </label>
            <Input
              id="proj-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("workspaceCrud.project.descriptionPlaceholder")}
            />
          </div>

          {errMsg && (
            <p className="rounded-md bg-red-50 px-3 py-2 text-[12px] text-red-700">
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
          <p className="rounded-md bg-red-50 px-3 py-2 text-[12px] text-red-700">
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
              className="text-[12px] font-medium text-slate-700"
              htmlFor="join-reason"
            >
              {t("workspaceCrud.fields.reason")}
              <span className="ml-1 text-[11px] font-normal text-slate-400">
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
              className="rounded-md border border-slate-200 px-3 py-2 text-[12px] outline-none focus:border-slate-400"
            />
            {tooLong && (
              <p className="text-[11px] text-red-600">
                {t("workspaceCrud.join.reasonTooLong")}
              </p>
            )}
          </div>

          {errMsg && (
            <p className="rounded-md bg-red-50 px-3 py-2 text-[12px] text-red-700">
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
