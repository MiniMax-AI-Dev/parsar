import { useState } from "react"
import { useTranslation } from "react-i18next"
import { CheckCircle2, KeyRound, Loader2, Shield } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
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
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog"
import { Skeleton } from "../ui/skeleton"
import { ApiError } from "../../lib/api-client"
import {
  useClearRuntimeCredential,
  useRuntimeStatus,
  useSaveRuntimeCredential,
} from "../../lib/api-runtime"

interface RuntimeCredentialCardProps {
  workspaceID: string | null
  variant?: "card" | "inline"
  /** When false, hide mutation buttons but keep the state row visible. */
  isAdmin: boolean
}

export function RuntimeCredentialCard({ workspaceID, isAdmin, variant = "card" }: RuntimeCredentialCardProps) {
  const { t } = useTranslation("admin")
  const statusQ = useRuntimeStatus(workspaceID)
  const saveMut = useSaveRuntimeCredential(workspaceID)
  const clearMut = useClearRuntimeCredential(workspaceID)
  const [saveOpen, setSaveOpen] = useState(false)
  const [manageOpen, setManageOpen] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)

  if (statusQ.isLoading) {
    if (variant === "inline") {
      return <Skeleton className="h-9 w-40 rounded-md" data-testid="runtime-credential-inline-loading" />
    }
    return (
      <section className="rounded-lg border border-slate-200 bg-white p-4" data-testid="runtime-credential-card-loading">
        <div className="flex items-center gap-2">
          <Skeleton className="h-5 w-32" />
        </div>
        <Skeleton className="mt-3 h-12 w-full" />
      </section>
    )
  }

  // RuntimeStatusBanner already surfaces "unreachable"; suppress here
  // to avoid duplicating the warning.
  if (statusQ.error || !statusQ.data) {
    return null
  }

  const hasCredential = statusQ.data.has_credential
  const masked = statusQ.data.credential_masked

  if (variant === "inline") {
    return (
      <>
        <Button
          size="sm"
          variant="outline"
          onClick={() => setManageOpen(true)}
          data-testid="runtime-credential-manage"
        >
          <KeyRound className="h-3.5 w-3.5" strokeWidth={1.75} />
          {hasCredential
            ? t("runtime.credential.actions.manageSaved", { value: masked ?? "•••" })
            : t("runtime.credential.actions.manageMissing")}
        </Button>
        <CredentialManageDialog
          hasCredential={hasCredential}
          isAdmin={isAdmin}
          masked={masked}
          onDelete={() => setConfirmClear(true)}
          onOpenChange={setManageOpen}
          onSave={() => setSaveOpen(true)}
          open={manageOpen}
        />
        <SaveDialog
          key={String(saveOpen)}
          open={saveOpen}
          onOpenChange={(open) => {
            setSaveOpen(open)
            if (!open) saveMut.reset()
          }}
          pending={saveMut.isPending}
          error={saveMut.error}
          existing={hasCredential}
          onSubmit={(payload) =>
            saveMut.mutate(payload, {
              onSuccess: () => {
                setSaveOpen(false)
                setManageOpen(false)
              },
            })
          }
        />
        <ClearCredentialDialog
          clearMut={clearMut}
          confirmClear={confirmClear}
          setConfirmClear={setConfirmClear}
        />
      </>
    )
  }

  return (
    <section className="rounded-lg border border-slate-200 bg-white p-4" data-testid="runtime-credential-card">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Shield className="h-4 w-4 text-slate-500" strokeWidth={1.75} />
            <h3 className="text-[14px] font-medium text-slate-900">
              {t("runtime.credential.title")}
            </h3>
          </div>
          <p className="mt-1 text-[13px] leading-relaxed text-slate-500 max-w-xl">
            {t("runtime.credential.description")}
          </p>
        </div>
        {isAdmin && (
          <div className="flex flex-wrap items-center gap-2">
            {hasCredential ? (
              <>
                <Button size="sm" variant="outline" onClick={() => setSaveOpen(true)} data-testid="runtime-credential-reset">
                  {t("runtime.credential.actions.reset")}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirmClear(true)} data-testid="runtime-credential-delete">
                  {t("runtime.credential.actions.delete")}
                </Button>
              </>
            ) : (
              <Button size="sm" onClick={() => setSaveOpen(true)} data-testid="runtime-credential-save">
                {t("runtime.credential.actions.save")}
              </Button>
            )}
          </div>
        )}
      </div>
      <div className="mt-3 rounded-md border border-slate-200 bg-slate-50/60 px-3 py-2 text-[13px]">
        {hasCredential ? (
          <div className="flex items-center gap-2 text-slate-700">
            <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600" strokeWidth={2} />
            <span>{t("runtime.credential.state.hasCredential")}</span>
            <code className="font-mono text-slate-900">{masked ?? "•••"}</code>
          </div>
        ) : (
          <div className="flex items-center gap-2 text-slate-600">
            <KeyRound className="h-3.5 w-3.5 text-slate-500" strokeWidth={1.75} />
            <span>{t("runtime.credential.state.noCredential")}</span>
          </div>
        )}
      </div>

      <SaveDialog
        key={String(saveOpen)}
        open={saveOpen}
        onOpenChange={(open) => {
          setSaveOpen(open)
          if (!open) saveMut.reset()
        }}
        pending={saveMut.isPending}
        error={saveMut.error}
        existing={hasCredential}
        onSubmit={(payload) =>
          saveMut.mutate(payload, {
            onSuccess: () => setSaveOpen(false),
          })
        }
      />

      <ClearCredentialDialog
        clearMut={clearMut}
        confirmClear={confirmClear}
        setConfirmClear={setConfirmClear}
      />
    </section>
  )
}

function CredentialManageDialog({
  hasCredential,
  isAdmin,
  masked,
  onDelete,
  onOpenChange,
  onSave,
  open,
}: {
  hasCredential: boolean
  isAdmin: boolean
  masked?: string | null
  onDelete: () => void
  onOpenChange: (open: boolean) => void
  onSave: () => void
  open: boolean
}) {
  const { t } = useTranslation("admin")
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle>{t("runtime.credential.title")}</DialogTitle>
          <DialogDescription>{t("runtime.credential.description")}</DialogDescription>
        </DialogHeader>
        <div className="rounded-md border border-slate-200 bg-slate-50 px-3 py-2 text-[13px]">
          {hasCredential ? (
            <div className="flex items-center gap-2 text-slate-700">
              <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600" strokeWidth={2} />
              <span>{t("runtime.credential.state.hasCredential")}</span>
              <code className="font-mono text-slate-900">{masked ?? "•••"}</code>
            </div>
          ) : (
            <div className="flex items-center gap-2 text-slate-600">
              <KeyRound className="h-3.5 w-3.5 text-slate-500" strokeWidth={1.75} />
              <span>{t("runtime.credential.state.noCredential")}</span>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            {t("runtime.credential.actions.cancel")}
          </Button>
          {isAdmin && hasCredential && (
            <Button variant="ghost" size="sm" onClick={onDelete} data-testid="runtime-credential-delete">
              {t("runtime.credential.actions.delete")}
            </Button>
          )}
          {isAdmin && (
            <Button size="sm" onClick={onSave} data-testid={hasCredential ? "runtime-credential-reset" : "runtime-credential-save"}>
              {hasCredential ? t("runtime.credential.actions.reset") : t("runtime.credential.actions.save")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ClearCredentialDialog({
  clearMut,
  confirmClear,
  setConfirmClear,
}: {
  clearMut: ReturnType<typeof useClearRuntimeCredential>
  confirmClear: boolean
  setConfirmClear: (open: boolean) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("runtime.credential.delete.title")}</AlertDialogTitle>
          <AlertDialogDescription>{t("runtime.credential.delete.description")}</AlertDialogDescription>
        </AlertDialogHeader>
        {clearMut.error && (
          <p className="rounded-md bg-red-50 px-3 py-2 text-[13px] text-red-700">
            {clearMut.error instanceof ApiError ? clearMut.error.envelope.message : t("runtime.credential.error.generic")}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={clearMut.isPending}>
            {t("runtime.credential.actions.cancel")}
          </AlertDialogCancel>
          <AlertDialogAction
            onClick={(e) => {
              e.preventDefault()
              clearMut.mutate(undefined, {
                onSuccess: () => setConfirmClear(false),
              })
            }}
            disabled={clearMut.isPending}
            data-testid="runtime-credential-delete-confirm"
          >
            {clearMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("runtime.credential.delete.confirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

interface SaveDialogProps {
  open: boolean
  pending: boolean
  error: unknown
  existing: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (payload: { apiKey: string }) => void
}

function SaveDialog({ open, pending, error, existing, onOpenChange, onSubmit }: SaveDialogProps) {
  const { t } = useTranslation("admin")
  const [apiKey, setApiKey] = useState("")
  const canSubmit = apiKey.trim() !== "" && !pending
  const errMsg = error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null


  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {existing ? t("runtime.credential.save.titleReset") : t("runtime.credential.save.titleNew")}
          </DialogTitle>
          <DialogDescription>{t("runtime.credential.save.description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <FieldLabel
            label={t("runtime.credential.save.field.apiKey")}
            hint={t("runtime.credential.save.field.apiKeyHint")}
          >
            <input
              type="password"
              autoComplete="off"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder="e2b_..."
              className="w-full rounded-md border border-slate-300 px-3 py-2 text-[13px] font-mono outline-none focus:border-slate-500"
              data-testid="runtime-credential-api-key-input"
            />
          </FieldLabel>
          {errMsg && (
            <p className="rounded-md bg-red-50 px-3 py-2 text-[13px] text-red-700">{errMsg}</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" disabled={pending} onClick={() => onOpenChange(false)}>
            {t("runtime.credential.actions.cancel")}
          </Button>
          <Button
            size="sm"
            disabled={!canSubmit}
            onClick={() => onSubmit({ apiKey: apiKey.trim() })}
            data-testid="runtime-credential-save-confirm"
          >
            {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {existing ? t("runtime.credential.save.confirmReset") : t("runtime.credential.save.confirmNew")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function FieldLabel({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="text-[13px] font-medium text-slate-700">{label}</span>
      {hint && <span className="ml-1 text-[12px] text-slate-500">{hint}</span>}
      <div className="mt-1.5">{children}</div>
    </label>
  )
}
