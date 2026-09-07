import { useMemo, useState } from "react"
import { useQueries } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { AlertTriangle, Eye, EyeOff, Loader2 } from "lucide-react"

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
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
import { Field } from "../../../components/ui/label"
import { Select } from "../../../components/ui/select"
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
  const canSubmit = plaintext.trim().length > 0
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

  const toggleLabel = showPlaintext ? t("myCredentials.dialog.hide") : t("myCredentials.dialog.show")

  return (
    <Dialog open onOpenChange={(next) => { if (!next && !pending) onClose() }}>
      <DialogContent aria-describedby={undefined}>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>
              {mode === "create" ? t("myCredentials.dialog.createTitle") : t("myCredentials.dialog.editTitle")}
            </DialogTitle>
          </DialogHeader>

          <div className="space-y-3">
            <Field label={t("myCredentials.dialog.fields.kind")} htmlFor="credential-kind">
              <Select
                id="credential-kind"
                value={kind}
                onChange={(event) => setKind(event.target.value)}
                disabled={kindLocked}
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
              </Select>
            </Field>

            {mode === "edit" && !replaceToken ? (
              <Field label={t("myCredentials.dialog.fields.value")}>
                <div className="flex h-7 items-center justify-between gap-3 text-sm text-fg">
                  <span className="truncate">{t("myCredentials.dialog.tokenSet")}</span>
                  <Button type="button" variant="link" size="sm" className="h-auto shrink-0 p-0" onClick={() => setReplaceToken(true)}>
                    {t("myCredentials.dialog.replaceToken")}
                  </Button>
                </div>
              </Field>
            ) : (
              <Field label={t("myCredentials.dialog.fields.value")} htmlFor="credential-value">
                <div className="relative">
                  <Input
                    id="credential-value"
                    type={showPlaintext ? "text" : "password"}
                    value={plaintext}
                    onChange={(event) => setPlaintext(event.target.value)}
                    placeholder={placeholder}
                    autoComplete="off"
                    required={mode === "create"}
                    className="pr-8"
                  />
                  <button
                    type="button"
                    aria-label={toggleLabel}
                    title={toggleLabel}
                    onClick={() => setShowPlaintext((prev) => !prev)}
                    className="absolute right-1 top-1/2 inline-flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-fg-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
                  >
                    {showPlaintext ? <EyeOff className="h-3.5 w-3.5" strokeWidth={1.5} /> : <Eye className="h-3.5 w-3.5" strokeWidth={1.5} />}
                  </button>
                </div>
              </Field>
            )}

            {error && (
              <ErrorState title={t("myCredentials.dialog.errorTitle")} description={error.message} className="py-0" />
            )}
          </div>

          <DialogFooter>
            {seedMeta?.getUrl && (
              <Button asChild variant="link" size="sm" className="mr-auto px-0">
                <a href={seedMeta.getUrl} target="_blank" rel="noopener noreferrer">
                  {t("myCredentials.dialog.openProvider")}
                </a>
              </Button>
            )}
            <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={pending}>
              {t("myCredentials.dialog.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={pending || !canSubmit}>
              {pending && <Loader2 className="animate-spin" />}
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
      <AlertDialogContent>
        <AlertDialogHeader className="flex-row items-start gap-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <div className="min-w-0 space-y-1.5">
            <AlertDialogTitle>{t("myCredentials.delete.title", { kind: kindLabel })}</AlertDialogTitle>
            <AlertDialogDescription className="leading-relaxed">
              {t("myCredentials.delete.description", { kind: kindLabel })}
            </AlertDialogDescription>
          </div>
        </AlertDialogHeader>

        <div className="pl-7">
          <h3 className="mb-1 text-xs font-medium text-fg">{t("myCredentials.delete.impactTitle")}</h3>
          {loadingImpact ? (
            <Skeleton className="h-3 w-44" />
          ) : (
            <p className="text-sm text-fg">
              {impact.length === 0
                ? t("myCredentials.delete.noImpact")
                : t("myCredentials.delete.hasImpact", { count: impact.length, workspaceCount })}
            </p>
          )}
          {error && (
            <p className="mt-2 flex items-start gap-1.5 text-sm text-fg">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
              <span className="break-words font-mono text-xs">{error.message}</span>
            </p>
          )}
        </div>

        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button variant="outline" size="sm" onClick={onCancel} disabled={pending}>
              {t("myCredentials.delete.cancel")}
            </Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button variant="destructive" size="sm" onClick={() => void onConfirm()} disabled={pending}>
              {pending && <Loader2 className="animate-spin" />}
              {t("myCredentials.delete.confirm")}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
