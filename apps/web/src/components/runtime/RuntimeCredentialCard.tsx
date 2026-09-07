import { useState } from "react"
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
import { Field } from "../ui/label"
import { Input } from "../ui/input"
import { InlineError } from "./InlineError"
import { PropertyList, Property } from "../ui/property-list"
import { Skeleton } from "../ui/skeleton"
import { ApiError } from "../../lib/api-client"
import {
  useClearRuntimeCredential,
  useRuntimeStatus,
  useSaveRuntimeCredential,
} from "../../lib/api-runtime"

interface RuntimeCredentialCardProps {
  workspaceID: string | null
  /** When false, hide mutation buttons but keep the state row visible. */
  isAdmin: boolean
  className?: string
}

/**
 * Workspace sandbox credential: a 12px/500 head with the actions on its
 * right, then a property row with the masked key.
 */
export function RuntimeCredentialCard({ workspaceID, isAdmin, className }: RuntimeCredentialCardProps) {
  const { t } = useTranslation("admin")
  const statusQ = useRuntimeStatus(workspaceID)
  const saveMut = useSaveRuntimeCredential(workspaceID)
  const clearMut = useClearRuntimeCredential(workspaceID)
  const [saveOpen, setSaveOpen] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)

  if (statusQ.isLoading) {
    return (
      <section className={className} data-testid="runtime-credential-card-loading">
        <Skeleton className="h-3 w-24" />
        <Skeleton className="mt-3 h-3 w-48" />
      </section>
    )
  }

  // RuntimeStatusBanner already surfaces "unreachable"; do not repeat it.
  if (statusQ.error || !statusQ.data) return null

  const hasCredential = statusQ.data.has_credential
  const masked = statusQ.data.credential_masked

  return (
    <section className={className} data-testid="runtime-credential-card">
      <div className="flex h-7 items-center justify-between gap-2">
        <h2 className="text-xs font-medium text-fg">{t("runtime.credential.title")}</h2>
        {isAdmin && (
          <div className="flex items-center gap-1">
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
              <Button size="sm" variant="outline" onClick={() => setSaveOpen(true)} data-testid="runtime-credential-save">
                {t("runtime.credential.actions.save")}
              </Button>
            )}
          </div>
        )}
      </div>
      <PropertyList>
        <Property
          label={hasCredential ? t("runtime.credential.state.hasCredential") : t("runtime.credential.state.noCredential")}
          mono
        >
          {hasCredential ? masked ?? "•••" : "—"}
        </Property>
      </PropertyList>

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
        onSubmit={(payload) => saveMut.mutate(payload, { onSuccess: () => setSaveOpen(false) })}
      />

      <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("runtime.credential.delete.title")}</AlertDialogTitle>
            <AlertDialogDescription>{t("runtime.credential.delete.description")}</AlertDialogDescription>
          </AlertDialogHeader>
          {clearMut.error && (
            <InlineError>
              {clearMut.error instanceof ApiError ? clearMut.error.envelope.message : t("runtime.credential.error.generic")}
            </InlineError>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button variant="outline" disabled={clearMut.isPending}>
                {t("runtime.credential.actions.cancel")}
              </Button>
            </AlertDialogCancel>
            <AlertDialogAction asChild>
              <Button
                variant="destructive"
                onClick={(e) => {
                  e.preventDefault()
                  clearMut.mutate(undefined, { onSuccess: () => setConfirmClear(false) })
                }}
                disabled={clearMut.isPending}
                data-testid="runtime-credential-delete-confirm"
              >
                {clearMut.isPending && <Loader2 className="animate-spin" />}
                {t("runtime.credential.delete.confirm")}
              </Button>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
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
  const hint = t("runtime.credential.save.field.apiKeyHint")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {existing ? t("runtime.credential.save.titleReset") : t("runtime.credential.save.titleNew")}
          </DialogTitle>
          <DialogDescription>{t("runtime.credential.save.description")}</DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (canSubmit) onSubmit({ apiKey: apiKey.trim() })
          }}
        >
          <Field label={t("runtime.credential.save.field.apiKey")} htmlFor="runtime-credential-api-key" hint={hint || undefined}>
            <Input
              id="runtime-credential-api-key"
              type="password"
              autoComplete="off"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder="e2b_..."
              className="font-mono"
              data-testid="runtime-credential-api-key-input"
            />
          </Field>
          {errMsg && <InlineError>{errMsg}</InlineError>}
        </form>
        <DialogFooter>
          <Button variant="outline" disabled={pending} onClick={() => onOpenChange(false)}>
            {t("runtime.credential.actions.cancel")}
          </Button>
          <Button
            disabled={!canSubmit}
            onClick={() => onSubmit({ apiKey: apiKey.trim() })}
            data-testid="runtime-credential-save-confirm"
          >
            {pending && <Loader2 className="animate-spin" />}
            {existing ? t("runtime.credential.save.confirmReset") : t("runtime.credential.save.confirmNew")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
