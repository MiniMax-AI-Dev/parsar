import { useMemo, useState } from "react"
import { useQueries } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { Eye, EyeOff, Loader2, ShieldAlert } from "lucide-react"

import { Button } from "../../../components/ui/button"
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { Input } from "../../../components/ui/input"
import { Skeleton } from "../../../components/ui/skeleton"
import {
  ApiError,
  apiRequest,
  noUnreachableRetry,
} from "../../../lib/api-client"
import { KEY_CAPABILITIES } from "../../../lib/api-capabilities"
import { useMyWorkspaces } from "../../../lib/api-workspaces"
import type {
  Capability,
  UserCredential,
  UserCredentialCreateRequest,
  UserCredentialPatchRequest,
} from "../../../lib/api-types"
import {
  CREDENTIAL_KIND_META,
  CREDENTIAL_KIND_OPTIONS,
  credentialKindLabel,
  useCredentialKindOptions,
  type KnownCredentialKind,
} from "../../../lib/credential-kind-ui"
import { useWorkspaceId } from "../../../lib/workspace"

interface CredentialDialogProps {
  mode: "create" | "edit"
  initialKind?: string | null
  credential?: UserCredential
  pending: boolean
  error?: ApiError
  onClose: () => void
  onSubmit: (body: UserCredentialCreateRequest | UserCredentialPatchRequest) => Promise<void>
}

export function CredentialDialog({
  mode,
  initialKind,
  credential,
  pending,
  error,
  onClose,
  onSubmit,
}: CredentialDialogProps) {
  const { t, i18n } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const kindOptions = useCredentialKindOptions(wsId)
  const fixedKind = mode === "edit" ? credential?.kind : initialKind
  // Edit mode keeps the credential's existing kind even if it's missing
  // from the live options list — the select is disabled anyway.
  const firstKind: string =
    (fixedKind as string | null | undefined) ??
    kindOptions.options[0] ??
    CREDENTIAL_KIND_OPTIONS[0]
  const [kind, setKind] = useState<string>(firstKind)
  const [plaintext, setPlaintext] = useState("")
  const [showPlaintext, setShowPlaintext] = useState(false)
  const [replaceToken, setReplaceToken] = useState(mode === "create")

  const kindLocked = mode === "edit" || !!initialKind
  const canSubmit = mode === "create"
    ? plaintext.trim().length > 0
    : plaintext.trim().length > 0
  const seedMeta = CREDENTIAL_KIND_META[kind as KnownCredentialKind]
  const placeholder = seedMeta
    ? (i18n.language.toLowerCase().startsWith("zh") ? seedMeta.placeholder.zh : seedMeta.placeholder.en)
    : ""

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (mode === "create") {
      await onSubmit({ kind, display_name: "", plaintext_value: plaintext })
      return
    }
    const body: UserCredentialPatchRequest = {}
    if (plaintext.trim()) body.plaintext_value = plaintext
    await onSubmit(body)
  }

  return (
    <Dialog open onOpenChange={(next) => { if (!next && !pending) onClose() }}>
      <DialogContent className="max-w-lg gap-0 p-0">
        <form onSubmit={submit}>
          <DialogHeader className="border-b border-line-muted px-5 py-4 pr-10">
            <DialogTitle className="text-sm">
              {mode === "create" ? t("myCredentials.dialog.createTitle") : t("myCredentials.dialog.editTitle")}
            </DialogTitle>
            <DialogDescription>{t("myCredentials.dialog.description")}</DialogDescription>
          </DialogHeader>

          <div className="space-y-4 px-5 py-4">
            <Field label={t("myCredentials.dialog.fields.kind")} required hint={t("myCredentials.dialog.fields.kindHint")}>
              <select
                value={kind}
                onChange={(event) => setKind(event.target.value)}
                disabled={kindLocked}
                className="h-9 w-full rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong disabled:bg-surface-subtle disabled:text-fg-subtle"
              >
                {/* A locked kind may not be in the live options list
                    (legacy data, admin-added kind, in-flight prefill);
                    render it explicitly so the field shows a label. */}
                {kindLocked && !kindOptions.options.includes(kind) && (
                  <option value={kind}>
                    {credentialKindLabel(kind, i18n.language, kind, kindOptions.kinds)}
                  </option>
                )}
                {kindOptions.options.map((option) => (
                  <option key={option} value={option}>
                    {credentialKindLabel(option, i18n.language, option, kindOptions.kinds)}
                  </option>
                ))}
              </select>
            </Field>

            {seedMeta?.getUrl && (
              <a className="inline-flex text-sm text-fg-muted underline-offset-4 hover:underline" href={seedMeta.getUrl} target="_blank" rel="noopener noreferrer">
                {t("myCredentials.dialog.openProvider")}
              </a>
            )}

            {mode === "edit" && !replaceToken ? (
              <div className="rounded-md border border-line bg-surface-subtle px-3 py-2">
                <div className="flex items-center justify-between gap-3">
                  <span className="text-sm text-fg-muted">{t("myCredentials.dialog.tokenSet")}</span>
                  <Button type="button" variant="link" size="sm" className="h-auto p-0" onClick={() => setReplaceToken(true)}>
                    {t("myCredentials.dialog.replaceToken")}
                  </Button>
                </div>
              </div>
            ) : (
              <Field label={t("myCredentials.dialog.fields.value")} hint={t("myCredentials.dialog.fields.valueHint")} required={mode === "create"}>
                <div className="flex gap-2">
                  <Input
                    type={showPlaintext ? "text" : "password"}
                    value={plaintext}
                    onChange={(event) => setPlaintext(event.target.value)}
                    placeholder={placeholder}
                    autoComplete="off"
                    required={mode === "create"}
                  />
                  <Button type="button" variant="outline" size="sm" className="h-9" onClick={() => setShowPlaintext((prev) => !prev)}>
                    {showPlaintext ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                    {showPlaintext ? t("myCredentials.dialog.hide") : t("myCredentials.dialog.show")}
                  </Button>
                </div>
              </Field>
            )}

            {error && (
              <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2">
                <p className="text-sm font-medium text-danger-emphasis">{t("myCredentials.dialog.errorTitle")}</p>
                <p className="text-xs text-danger-emphasis">{error.message}</p>
              </div>
            )}
          </div>

          <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
            <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={pending}>
              {t("myCredentials.dialog.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={pending || !canSubmit}>
              {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t("myCredentials.dialog.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

interface DeleteCredentialDialogProps {
  target: UserCredential
  pending: boolean
  error?: ApiError
  onCancel: () => void
  onConfirm: () => Promise<void>
}

export function DeleteCredentialDialog({ target, pending, error, onCancel, onConfirm }: DeleteCredentialDialogProps) {
  const { t, i18n } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const kindOptions = useCredentialKindOptions(wsId)
  const workspacesQ = useMyWorkspaces()
  const workspaces = workspacesQ.data?.workspaces ?? []
  const capabilityQueries = useQueries({
    queries: workspaces.map((workspace) => ({
      queryKey: KEY_CAPABILITIES(workspace.id),
      queryFn: () => apiRequest<{ capabilities: Capability[] }>(`/api/v1/workspaces/${encodeURIComponent(workspace.id)}/capabilities`),
      enabled: workspaces.length > 0,
      retry: noUnreachableRetry,
      staleTime: 0,
    })),
  })

  const impact = useMemo(() => {
    const rows: Array<{ workspaceName: string; capabilityName: string }> = []
    capabilityQueries.forEach((query, idx) => {
      for (const capability of query.data?.capabilities ?? []) {
        if (capability.status === "active" && (capability.required_credentials ?? []).some((rc) => rc.kind === target.kind)) {
          rows.push({ workspaceName: workspaces[idx]?.name ?? "", capabilityName: capability.name })
        }
      }
    })
    return rows
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [capabilityQueries.map((query) => query.dataUpdatedAt).join(":"), target.kind, workspaces])

  const loadingImpact = workspacesQ.isLoading || capabilityQueries.some((query) => query.isLoading)
  const workspaceCount = new Set(impact.map((item) => item.workspaceName)).size
  const kindLabel = credentialKindLabel(target.kind, i18n.language, t("myCredentials.kind.unknown"), kindOptions.kinds)

  return (
    <AlertDialog open onOpenChange={(next) => { if (!next && !pending) onCancel() }}>
      <AlertDialogContent className="max-w-md gap-0 p-0">
        <AlertDialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5">
          <div className="shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis">
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div className="space-y-2">
            <AlertDialogTitle className="text-sm">{t("myCredentials.delete.title", { kind: kindLabel })}</AlertDialogTitle>
            <AlertDialogDescription className="text-sm leading-relaxed">
              {t("myCredentials.delete.description", { kind: kindLabel })}
            </AlertDialogDescription>
            <div className="rounded-md border border-line bg-surface-subtle p-3">
              <p className="text-sm font-medium text-fg-emphasis">{t("myCredentials.delete.impactTitle")}</p>
              {loadingImpact ? (
                <div className="mt-2 space-y-2">
                  <Skeleton className="h-4 w-44" />
                  <p className="text-xs text-fg-subtle">{t("myCredentials.delete.loadingImpact")}</p>
                </div>
              ) : impact.length === 0 ? (
                <p className="mt-2 text-sm text-fg-muted">{t("myCredentials.delete.noImpact")}</p>
              ) : (
                <p className="mt-2 text-sm text-danger-emphasis">
                  {t("myCredentials.delete.hasImpact", { count: impact.length, workspaceCount })}
                </p>
              )}
            </div>
            {error && <p className="text-sm text-danger-emphasis">{error.message}</p>}
          </div>
        </AlertDialogHeader>
        <AlertDialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
          <AlertDialogCancel asChild>
            <Button variant="outline" size="sm" onClick={onCancel} disabled={pending}>
              {t("myCredentials.delete.cancel")}
            </Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button variant="destructive" size="sm" onClick={() => void onConfirm()} disabled={pending}>
              {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t("myCredentials.delete.confirm")}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function Field({
  label,
  hint,
  required,
  children,
}: {
  label: string
  hint?: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <label className="block space-y-1">
      <span className="text-sm font-medium text-fg-muted">
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </span>
      {children}
      {hint && <span className="block text-xs text-fg-subtle">{hint}</span>}
    </label>
  )
}
