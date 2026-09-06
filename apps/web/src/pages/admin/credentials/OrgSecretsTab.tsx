import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowUpRight, Ban, Loader2 } from "lucide-react"

import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
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
import { Field } from "../../../components/ui/label"
import {
  Ledger,
  LedgerGroup,
  LedgerHeader,
  LedgerId,
  LedgerRow,
  col,
} from "../../../components/ui/ledger"
import { Select } from "../../../components/ui/select"
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
  /** Header search term; matches name, slug, provider and kind. */
  query?: string
  /** Increments each time the page-level "add" button is pressed. */
  createRequest?: number
}

/** name · kind (dot = status) · provider · masked · updated · actions */
const LEDGER_COLUMNS = [col.title(), col.meta(120), col.meta(120), col.id(128), col.age(80), col.actions(1)]

function kindLabel(kind: string) {
  if (kind === "model_provider") return "Model API Key"
  if (kind === "runtime" || kind === "e2b") return "Runtime"
  if (kind.startsWith("feishu")) return "Feishu"
  return "API Key"
}

export function OrgSecretsTab({ workspaceID, query = "", createRequest = 0 }: OrgSecretsTabProps) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const navigate = useNavigateAdmin()
  const secretsQ = useSecrets(workspaceID)
  const disableMut = useDisableSecret(workspaceID)
  const createMut = useCreateSecret(workspaceID)

  // The page header owns the "add" button and bumps `createRequest`; any
  // bump we have not yet dismissed counts as an open dialog.
  const [dismissedRequest, setDismissedRequest] = useState(createRequest)
  const [confirmTarget, setConfirmTarget] = useState<Secret | null>(null)
  const createOpen = createRequest > dismissedRequest
  const closeCreate = () => setDismissedRequest(createRequest)

  const secrets = useMemo(() => {
    const all = secretsQ.data?.secrets ?? []
    const needle = query.trim().toLowerCase()
    if (!needle) return all
    return all.filter((s) =>
      [s.name, s.slug, s.provider, s.kind, kindLabel(s.kind)].join(" ").toLowerCase().includes(needle),
    )
  }, [secretsQ.data?.secrets, query])
  const modelKeys = useMemo(() => secrets.filter((s) => s.kind === "model_provider"), [secrets])
  const runtimeKeys = useMemo(() => secrets.filter((s) => s.kind === "runtime" || s.provider === "e2b"), [secrets])
  const otherKeys = useMemo(() => secrets.filter((s) => s.kind !== "model_provider" && s.kind !== "runtime" && s.provider !== "e2b"), [secrets])
  const errorObj = secretsQ.error as ApiError | undefined

  if (secretsQ.isLoading) {
    return (
      <div className="px-4 pt-3">
        <div className="mb-3 h-7 border-b border-line" />
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
            <Skeleton className="h-3 w-40" />
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-3 flex-1" />
            <Skeleton className="h-3 w-16" />
          </div>
        ))}
      </div>
    )
  }

  if (secretsQ.isError) {
    return (
      <div className="px-4 pt-4">
        <ErrorState
          title={errorObj?.envelope?.unreachable ? t("secrets.error.unreachable.title") : t("secrets.error.load.title")}
          description={errorObj?.envelope?.unreachable ? t("secrets.error.unreachable.description") : errorObj?.message ?? t("secrets.error.load.description")}
          hint={errorObj?.envelope?.unreachable ? t("secrets.error.unreachable.hint") : t("secrets.error.load.hint")}
          onRetry={() => void secretsQ.refetch()}
        />
      </div>
    )
  }

  return (
    <>
      <Ledger columns={LEDGER_COLUMNS} role="list" aria-label={t("credentialsPage.tabs.org")}>
        <LedgerHeader>
          <span>{t("secrets.create.field.name")}</span>
          <span>{t("myCredentials.table.kind")}</span>
          <span>{t("secrets.create.field.provider")}</span>
          <span>{t("secrets.create.field.apiKey")}</span>
          <span className="text-right">{t("myCredentials.table.lastUsed")}</span>
          <span />
        </LedgerHeader>

        {/* Model API Keys — read-only. Rotation lives on the Models page. */}
        <SecretGroup
          label={t("secrets.sections.modelKeys")}
          items={modelKeys}
          empty={t("secrets.empty.modelKeys")}
          fmtAgo={fmtAgo}
          readOnlyLabel={t("credentialsPage.org.openModels")}
          onOpenModels={() => navigate("models")}
          onDisable={setConfirmTarget}
        />
        <SecretGroup
          label={t("secrets.sections.runtimeKeys")}
          items={runtimeKeys}
          empty={t("secrets.empty.runtimeKeys")}
          fmtAgo={fmtAgo}
          onDisable={setConfirmTarget}
        />
        {otherKeys.length > 0 && (
          <SecretGroup
            label={t("secrets.sections.otherKeys")}
            items={otherKeys}
            fmtAgo={fmtAgo}
            onDisable={setConfirmTarget}
          />
        )}
      </Ledger>

      {createOpen && (
        <CreateDialog
          onClose={() => {
            closeCreate()
            createMut.reset()
          }}
          onSubmit={async (input) => {
            await createMut.mutateAsync(input)
            closeCreate()
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
    </>
  )
}

interface GroupProps {
  label: string
  items: Secret[]
  empty?: string
  fmtAgo: (iso: string) => string
  onDisable: (secret: Secret) => void
  /** Present on the Model API Keys group: rows link to Models instead of
   * offering Disable, because rotation lives there. */
  readOnlyLabel?: string
  onOpenModels?: () => void
}

function SecretGroup({ label, items, empty, fmtAgo, onDisable, readOnlyLabel, onOpenModels }: GroupProps) {
  const { t } = useTranslation("admin")
  return (
    <LedgerGroup label={label} count={items.length}>
      {items.length === 0 && empty ? (
        <li className="flex h-9 items-center border-b border-line px-4 text-sm text-fg-muted">{empty}</li>
      ) : (
        items.map((secret) => {
          const active = secret.status === "active"
          const statusLabel = active ? t("secrets.status.active") : t("secrets.status.disabled")
          return (
            <LedgerRow key={secret.id} role="listitem" tabIndex={-1}>
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate font-medium">{secret.name}</span>
                {secret.slug && <LedgerId className="shrink-0">{secret.slug}</LedgerId>}
              </span>
              <span className="min-w-0">
                <Badge variant={active ? "success" : "neutral"} dot title={statusLabel}>
                  {kindLabel(secret.kind)}
                </Badge>
              </span>
              <span className="truncate text-xs text-fg-muted">{secret.provider || t("secrets.none")}</span>
              <LedgerId>{secret.masked}</LedgerId>
              <span className="truncate text-right text-xs text-fg-muted">{fmtAgo(secret.updated_at)}</span>
              <RowActions>
                {readOnlyLabel && onOpenModels ? (
                  <ActionIconButton icon={ArrowUpRight} label={readOnlyLabel} onClick={onOpenModels} />
                ) : active ? (
                  <ActionIconButton icon={Ban} label={t("secrets.actions.disable")} tone="danger" onClick={() => onDisable(secret)} />
                ) : null}
              </RowActions>
            </LedgerRow>
          )
        })
      )}
    </LedgerGroup>
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
      <DialogContent aria-describedby={undefined}>
        <form onSubmit={handleSubmit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{t("credentialsPage.org.create.title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <Field label={t("secrets.create.field.purpose")} htmlFor="secret-purpose">
              <Select
                id="secret-purpose"
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
              </Select>
            </Field>
            <Field label={t("secrets.create.field.name")} htmlFor="secret-name">
              <Input id="secret-name" value={name} onChange={(event) => setName(event.target.value)} placeholder={t("secrets.create.placeholder.name")} required />
            </Field>
            <Field label={t("secrets.create.field.provider")} htmlFor="secret-provider">
              <Input id="secret-provider" value={provider} onChange={(event) => setProvider(event.target.value)} placeholder={purpose === "runtime" ? "e2b" : "stripe"} required />
            </Field>
            <Field label={t("secrets.create.field.apiKey")} htmlFor="secret-api-key">
              <Input id="secret-api-key" type="password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder="sk-..." autoComplete="off" required />
            </Field>
            {error && (
              <ErrorState title={t("secrets.create.error.title")} description={error.message} className="py-0" />
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={pending}>{t("secrets.create.cancel")}</Button>
            <Button type="submit" size="sm" disabled={pending}>
              {pending && <Loader2 className="animate-spin" />}
              {t("secrets.create.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function ConfirmDialog({ target, loading, error, onCancel, onConfirm }: { target: Secret; loading: boolean; error?: ApiError; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useTranslation("admin")
  return (
    <Dialog open onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <DialogContent showCloseButton={false}>
        <DialogHeader className="flex-row items-start gap-3 pr-0">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <div className="min-w-0 space-y-1.5">
            <DialogTitle>{t("secrets.disable.title", { name: target.name })}</DialogTitle>
            <DialogDescription className="leading-relaxed">{t("secrets.disable.description")}</DialogDescription>
            {error && <p className="break-words font-mono text-xs text-fg">{error.message}</p>}
          </div>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onCancel} disabled={loading}>{t("secrets.disable.cancel")}</Button>
          <Button variant="destructive" size="sm" onClick={onConfirm} disabled={loading}>
            {loading && <Loader2 className="animate-spin" />}
            {t("secrets.disable.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
