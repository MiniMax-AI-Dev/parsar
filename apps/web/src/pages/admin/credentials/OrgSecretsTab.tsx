import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  ArrowRight,
  Cloud,
  KeyRound,
  Loader2,
  Plus,
  ShieldAlert,
} from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
import { Skeleton } from "../../../components/ui/skeleton"
import { ApiError } from "../../../lib/api-client"
import {
  useCreateSecret,
  useDisableSecret,
  useSecrets,
} from "../../../lib/api-secrets"
import type {
  CreateSecretRequest,
  Secret,
} from "../../../lib/api-types"
import { useNavigateAdmin } from "../../../lib/admin-router"
import { useRelativeTime } from "../../../lib/relative-time"

interface OrgSecretsTabProps {
  workspaceID: string
}

function StatusBadge({ status }: { status: Secret["status"] }) {
  const { t } = useTranslation("admin")
  if (status === "active") return <Badge variant="success" dot>{t("secrets.status.active")}</Badge>
  return <Badge variant="neutral" dot>{t("secrets.status.disabled")}</Badge>
}

function kindLabel(kind: string) {
  if (kind === "model_provider") return "Model API Key"
  if (kind === "runtime" || kind === "e2b") return "Runtime"
  if (kind.startsWith("feishu")) return "Feishu"
  return "API Key"
}

function usageText(secret: Secret, t: ReturnType<typeof useTranslation<"admin">>["t"]) {
  if (secret.kind === "model_provider") return t("secrets.usage.modelProvider")
  if (secret.kind === "runtime" || secret.provider === "e2b") return t("secrets.usage.runtime")
  if (secret.kind.startsWith("feishu")) return t("secrets.usage.feishu")
  return t("secrets.usage.custom")
}

export function OrgSecretsTab({ workspaceID }: OrgSecretsTabProps) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const navigate = useNavigateAdmin()
  const secretsQ = useSecrets(workspaceID)
  const disableMut = useDisableSecret(workspaceID)
  const createMut = useCreateSecret(workspaceID)

  const [createOpen, setCreateOpen] = useState(false)
  const [confirmTarget, setConfirmTarget] = useState<Secret | null>(null)

  const secrets = secretsQ.data?.secrets ?? []
  const modelKeys = useMemo(() => secrets.filter((s) => s.kind === "model_provider"), [secrets])
  const runtimeKeys = useMemo(() => secrets.filter((s) => s.kind === "runtime" || s.provider === "e2b"), [secrets])
  const otherKeys = useMemo(() => secrets.filter((s) => s.kind !== "model_provider" && s.kind !== "runtime" && s.provider !== "e2b"), [secrets])
  const errorObj = secretsQ.error as ApiError | undefined

  if (secretsQ.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-32 rounded-lg" />
        <Skeleton className="h-48 rounded-lg" />
      </div>
    )
  }

  if (secretsQ.isError) {
    return (
      <ErrorState
        title={errorObj?.envelope?.unreachable ? t("secrets.error.unreachable.title") : t("secrets.error.load.title")}
        description={errorObj?.envelope?.unreachable ? t("secrets.error.unreachable.description") : errorObj?.message ?? t("secrets.error.load.description")}
        hint={errorObj?.envelope?.unreachable ? t("secrets.error.unreachable.hint") : t("secrets.error.load.hint")}
        onRetry={() => void secretsQ.refetch()}
      />
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-3">
        <p className="text-[13px] leading-relaxed text-slate-500">
          {t("credentialsPage.org.scopeNote")}
        </p>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          {t("credentialsPage.org.add")}
        </Button>
      </div>

      {/* Model API Keys — read-only. Rotation lives on the Models page. */}
      <CredentialSection
        title={t("secrets.sections.modelKeys")}
        description={t("secrets.sections.modelKeysDescription")}
        icon={KeyRound}
        items={modelKeys}
        empty={t("secrets.empty.modelKeys")}
        fmtAgo={fmtAgo}
        readOnly
        readOnlyAction={
          <Button size="sm" variant="outline" onClick={() => navigate("models")}>
            {t("credentialsPage.org.openModels")}
            <ArrowRight className="h-3.5 w-3.5" />
          </Button>
        }
        onDisable={(secret) => setConfirmTarget(secret)}
      />

      <CredentialSection
        title={t("secrets.sections.runtimeKeys")}
        description={t("secrets.sections.runtimeKeysDescription")}
        icon={Cloud}
        items={runtimeKeys}
        empty={t("secrets.empty.runtimeKeys")}
        fmtAgo={fmtAgo}
        onDisable={(secret) => setConfirmTarget(secret)}
      />

      {otherKeys.length > 0 && (
        <CredentialSection
          title={t("secrets.sections.otherKeys")}
          description={t("secrets.sections.otherKeysDescription")}
          icon={KeyRound}
          items={otherKeys}
          empty=""
          fmtAgo={fmtAgo}
          onDisable={(secret) => setConfirmTarget(secret)}
        />
      )}

      {createOpen && (
        <CreateDialog
          onClose={() => {
            setCreateOpen(false)
            createMut.reset()
          }}
          onSubmit={async (input) => {
            await createMut.mutateAsync(input)
            setCreateOpen(false)
            createMut.reset()
          }}
          pending={createMut.isPending}
          error={createMut.error as ApiError | undefined}
        />
      )}

      {confirmTarget && (
        <ConfirmDialog
          target={confirmTarget}
          loading={disableMut.isPending}
          error={disableMut.error as ApiError | undefined}
          onCancel={() => {
            setConfirmTarget(null)
            disableMut.reset()
          }}
          onConfirm={async () => {
            try {
              await disableMut.mutateAsync(confirmTarget.id)
              setConfirmTarget(null)
            } catch {
              // surfaced inline in the dialog
            }
          }}
        />
      )}
    </div>
  )
}

interface SectionProps {
  title: string
  description: string
  icon: typeof KeyRound
  items: Secret[]
  empty: string
  fmtAgo: (iso: string) => string
  onDisable: (secret: Secret) => void
  /** Hide per-row Disable. Set on Model API Keys section where
   * rotation lives on the Models page. */
  readOnly?: boolean
  /** Slot rendered next to the section count when `readOnly` is set. */
  readOnlyAction?: React.ReactNode
}

function CredentialSection({
  title,
  description,
  icon: Icon,
  items,
  empty,
  fmtAgo,
  onDisable,
  readOnly,
  readOnlyAction,
}: SectionProps) {
  const { t } = useTranslation("admin")
  return (
    <section className="rounded-lg border border-slate-200 bg-white">
      <div className="flex items-start justify-between gap-3 border-b border-slate-100 px-4 py-3">
        <div className="flex items-start gap-2">
          <Icon className="mt-0.5 h-4 w-4 text-slate-400" />
          <div>
            <h3 className="text-[14px] font-semibold text-slate-900">{title}</h3>
            <p className="mt-0.5 text-[13px] text-slate-500">{description}</p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <span className="text-[13px] text-slate-400">{items.length}</span>
          {readOnly && readOnlyAction}
        </div>
      </div>
      {items.length === 0 ? (
        <div className="px-4 py-8 text-center text-[13px] text-slate-500">{empty}</div>
      ) : (
        <div className="divide-y divide-slate-100">
          {items.map((secret) => (
            <div key={secret.id} className="flex items-center justify-between gap-4 px-4 py-3">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-[13px] font-medium text-slate-900">{secret.name}</span>
                  <StatusBadge status={secret.status} />
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-2 text-[13px] text-slate-500">
                  <span>{kindLabel(secret.kind)}</span>
                  <span>·</span>
                  <span>{secret.provider || t("secrets.none")}</span>
                  <span>·</span>
                  <code>{secret.masked}</code>
                  {secret.slug && (
                    <>
                      <span>·</span>
                      <code className="text-[12px] text-slate-400">{secret.slug}</code>
                    </>
                  )}
                </div>
                <p className="mt-1 text-[13px] text-slate-500">{usageText(secret, t)}</p>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <span className="text-[13px] text-slate-400">{fmtAgo(secret.updated_at)}</span>
                {!readOnly && (
                  secret.status === "active" ? (
                    <Button variant="outline" size="sm" onClick={() => onDisable(secret)}>{t("secrets.actions.disable")}</Button>
                  ) : (
                    <span className="text-[12px] text-slate-400">{t("secrets.actions.alreadyDisabled")}</span>
                  )
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}

interface CreateDialogProps {
  onClose: () => void
  onSubmit: (input: { body: CreateSecretRequest }) => Promise<void>
  pending: boolean
  error?: ApiError
}

function CreateDialog({ onClose, onSubmit, pending, error }: CreateDialogProps) {
  const { t } = useTranslation("admin")
  // Model service omitted — that path lives on the Models page
  // (CreateProviderDialog auto-creates the secret). A bare key with no
  // Provider attached cannot be resolved at runtime.
  const [purpose, setPurpose] = useState<"runtime" | "custom_api">("runtime")
  const [name, setName] = useState("")
  const [provider, setProvider] = useState("e2b")
  const [apiKey, setApiKey] = useState("")

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    await onSubmit({
      body: {
        name,
        kind: purpose,
        provider,
        auth_type: "api_key",
        payload: { api_key: apiKey },
      },
    })
  }

  return (
    <Dialog open onOpenChange={(next) => { if (!next && !pending) onClose() }}>
      <DialogContent className="max-w-lg gap-0 p-0">
        <form onSubmit={handleSubmit}>
          <DialogHeader className="border-b border-slate-100 px-5 py-4 pr-10">
            <DialogTitle className="text-sm">{t("credentialsPage.org.create.title")}</DialogTitle>
            <DialogDescription>{t("credentialsPage.org.create.description")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3 px-5 py-4">
            <Field label={t("secrets.create.field.purpose")} required>
              <select
                className="h-9 w-full rounded-md border border-slate-200 bg-white px-3 text-[13px]"
                value={purpose}
                onChange={(event) => {
                  const next = event.target.value as "runtime" | "custom_api"
                  setPurpose(next)
                  if (next === "runtime") setProvider("e2b")
                  else setProvider("")
                }}
              >
                <option value="runtime">{t("secrets.create.purpose.runtime")}</option>
                <option value="custom_api">{t("secrets.create.purpose.custom")}</option>
              </select>
            </Field>
            <Field label={t("secrets.create.field.name")} required>
              <Input value={name} onChange={(event) => setName(event.target.value)} placeholder={t("secrets.create.placeholder.name")} required />
            </Field>
            <Field label={t("secrets.create.field.provider")} required>
              <Input value={provider} onChange={(event) => setProvider(event.target.value)} placeholder={purpose === "runtime" ? "e2b" : "stripe"} required />
            </Field>
            <Field label={t("secrets.create.field.apiKey")} required>
              <Input type="password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder="sk-..." autoComplete="off" required />
            </Field>
            {error && (
              <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2">
                <p className="text-[13px] font-medium text-red-900">{t("secrets.create.error.title")}</p>
                <p className="text-[12px] text-red-700">{error.message}</p>
              </div>
            )}
          </div>
          <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
            <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={pending}>{t("secrets.create.cancel")}</Button>
            <Button type="submit" size="sm" disabled={pending}>{pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("secrets.create.submit")}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function Field({ label, hint, required, children }: { label: string; hint?: string; required?: boolean; children: React.ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-[13px] font-medium text-slate-700">{label}{required && <span className="ml-0.5 text-red-500">*</span>}</span>
      {children}
      {hint && <span className="block text-[12px] text-slate-500">{hint}</span>}
    </label>
  )
}

function ConfirmDialog({ target, loading, error, onCancel, onConfirm }: { target: Secret; loading: boolean; error?: ApiError; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useTranslation("admin")
  return (
    <Dialog open onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
        <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5">
          <div className="shrink-0 rounded-full bg-red-100 p-2 text-red-700"><ShieldAlert className="h-4 w-4" /></div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">{t("secrets.disable.title", { name: target.name })}</DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">{t("secrets.disable.description")}</DialogDescription>
            {error && <p className="text-[13px] text-red-700">{error.message}</p>}
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
          <Button variant="outline" size="sm" onClick={onCancel} disabled={loading}>{t("secrets.disable.cancel")}</Button>
          <Button variant="destructive" size="sm" onClick={onConfirm} disabled={loading}>{loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("secrets.disable.confirm")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
