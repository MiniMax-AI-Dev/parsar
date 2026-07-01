import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import QRCode from "qrcode"
import { AlertTriangle, CheckCircle2, ExternalLink, Loader2, MessageCircle, QrCode, RefreshCw, XCircle } from "lucide-react"

import { ApiError } from "../../lib/api-client"
import {
  useBeginAgentFeishuProvisioning,
  usePollAgentFeishuProvisioning,
  useUpdateAgentFeishuConnector,
  type FeishuConnectorConfig,
  type FeishuConnectorDiagnostics,
} from "../../lib/api-agents"
import { useCreateSecret } from "../../lib/api-secrets"
import type { CreateSecretRequest } from "../../lib/api-types"

function Card({
  title,
  description,
  className,
  children,
}: {
  title: string
  description?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <section className={`rounded-lg border border-line bg-surface p-4 ${className ?? ""}`}>
      <header className="mb-3">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-fg-subtle">{title}</h3>
        {description && <p className="mt-1 text-sm text-fg-subtle">{description}</p>}
      </header>
      {children}
    </section>
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
    <div className="mb-3 last:mb-0">
      <label className="mb-1 block text-xs uppercase tracking-wider text-fg-faint">
        {label}
        {required && <span className="ml-1 text-danger">*</span>}
      </label>
      {children}
      {hint && <p className="mt-1 text-xs text-fg-subtle">{hint}</p>}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  FeishuConnectorPanel — see docs/feishu-routing.md §6.2             */
/*                                                                     */
/*  Sensitive values are accepted once, written to the workspace       */
/*  Secret vault on save, and only the resulting *_ref pointers go     */
/*  into agent.config.                                                 */
/* ------------------------------------------------------------------ */

const EMPTY_CONFIG: FeishuConnectorConfig = {
  enabled: false,
  app_id: "",
  app_secret_ref: "",
  verification_token_ref: "",
  encrypt_key_ref: "",
  bot_open_id: "",
  event_mode: "webhook",
  routing_mode: "direct",
}

type SecretInputs = {
  appSecret: string
  verificationToken: string
  encryptKey: string
}

type FeishuSecretField = keyof SecretInputs
type FeishuSecretRefKey = "app_secret_ref" | "verification_token_ref" | "encrypt_key_ref"

type FeishuSecretFieldSpec = {
  refKey: FeishuSecretRefKey
  kind: string
  authType: string
  payloadKey: string
  namePrefix: string
}

const EMPTY_SECRET_INPUTS: SecretInputs = {
  appSecret: "",
  verificationToken: "",
  encryptKey: "",
}

const FEISHU_SECRET_FIELDS: Record<FeishuSecretField, FeishuSecretFieldSpec> = {
  appSecret: {
    refKey: "app_secret_ref",
    kind: "feishu_app_secret",
    authType: "app_secret",
    payloadKey: "app_secret",
    namePrefix: "feishu-app-secret",
  },
  verificationToken: {
    refKey: "verification_token_ref",
    kind: "feishu_verification_token",
    authType: "verification_token",
    payloadKey: "verification_token",
    namePrefix: "feishu-verification-token",
  },
  encryptKey: {
    refKey: "encrypt_key_ref",
    kind: "feishu_encrypt_key",
    authType: "encrypt_key",
    payloadKey: "encrypt_key",
    namePrefix: "feishu-encrypt-key",
  },
}

const SHOW_FEISHU_DIAGNOSTICS = false

type ProvisionState = {
  deviceCode: string
  userCode: string
  verificationUrl: string
  qrDataUrl: string
  expiresAt: number
  intervalSec: number
  status: "pending" | "success" | "error" | "expired"
  message?: string
}

interface FeishuConnectorPanelProps {
  agentID: string
  workspaceID: string | null
  /** Current persisted config — undefined when never configured. */
  current: FeishuConnectorConfig | undefined
  canEdit: boolean
  onToast: (msg: string) => void
}

export function FeishuConnectorPanel({
  agentID,
  workspaceID,
  current,
  canEdit,
  onToast,
}: FeishuConnectorPanelProps) {
  const { t } = useTranslation("admin")
  const mut = useUpdateAgentFeishuConnector(workspaceID)
  const createSecretMut = useCreateSecret(workspaceID)
  const beginProvisionMut = useBeginAgentFeishuProvisioning(workspaceID)
  const pollProvisionMut = usePollAgentFeishuProvisioning(workspaceID)

  // Local edit buffer so cancel doesn't ping the server. Re-seeded
  // when the persisted config changes (e.g. PATCH refetch).
  const [draft, setDraft] = useState<FeishuConnectorConfig>(current ?? EMPTY_CONFIG)
  const [secretInputs, setSecretInputs] = useState<SecretInputs>(emptySecretInputs())
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const [provision, setProvision] = useState<ProvisionState | null>(null)

  useEffect(() => {
    setDraft(current ?? EMPTY_CONFIG)
    setSecretInputs(emptySecretInputs())
    setErrorMsg(null)
  }, [current])

  const dirty = !configEqual(draft, current ?? EMPTY_CONFIG) || secretInputsDirty(secretInputs)
  const saving = mut.isPending || createSecretMut.isPending
  const pollProvisionRef = useRef(pollProvisionMut.mutate)
  pollProvisionRef.current = pollProvisionMut.mutate
  const pollProvisionPending = pollProvisionMut.isPending

  // Backend re-checks (422/409); pre-validate so the save button is honest.
  const missingRequired = draft.enabled && (
    !draft.app_id.trim() ||
    (!draft.app_secret_ref.trim() && !secretInputs.appSecret.trim()) ||
    (draft.event_mode !== "websocket" && !draft.verification_token_ref.trim() && !secretInputs.verificationToken.trim())
  )
  const entryMode = draft.enabled ? "dedicated" : "default"

  const onEntryModeChange = (mode: "default" | "dedicated") => {
    setErrorMsg(null)
    if (mode === "default") {
      setProvision(null)
      setDraft(EMPTY_CONFIG)
      setSecretInputs(emptySecretInputs())
      return
    }
    setDraft({ ...draft, enabled: true, routing_mode: "direct" })
  }

  useEffect(() => {
    if (!provision || provision.status !== "pending" || pollProvisionPending) return
    if (Date.now() >= provision.expiresAt) {
      setProvision({ ...provision, status: "expired", message: t("agents.feishuConnector.provision.expired") })
      return
    }
    const timer = window.setTimeout(() => {
      pollProvisionRef.current(
        {
          agentID,
          deviceCode: provision.deviceCode,
          intervalSec: provision.intervalSec,
        },
        {
          onSuccess: (res) => {
            if (res.status === "pending") {
              setProvision((prev) => prev ? {
                ...prev,
                intervalSec: res.next_interval_sec ?? prev.intervalSec,
              } : prev)
              return
            }
            if (res.status === "success") {
              if (res.feishu_connector?.new) {
                setDraft(res.feishu_connector.new)
                setSecretInputs(emptySecretInputs())
              }
              setProvision((prev) => prev ? {
                ...prev,
                status: "success",
                message: res.bot_name
                  ? t("agents.feishuConnector.provision.successWithName", { name: res.bot_name })
                  : t("agents.feishuConnector.provision.success"),
              } : prev)
              onToast(t("agents.feishuConnector.provision.saved"))
              return
            }
            const expired = res.error === "expired_token"
            setProvision((prev) => prev ? {
              ...prev,
              status: expired ? "expired" : "error",
              message: expired
                ? t("agents.feishuConnector.provision.expired")
                : res.description ?? res.error ?? t("agents.feishuConnector.provision.failed"),
            } : prev)
          },
          onError: (err) => {
            setProvision((prev) => prev ? {
              ...prev,
              status: "error",
              message: err instanceof ApiError ? err.envelope.message : t("agents.feishuConnector.provision.failed"),
            } : prev)
          },
        },
      )
    }, Math.max(1, provision.intervalSec) * 1000)
    return () => window.clearTimeout(timer)
  }, [agentID, onToast, pollProvisionPending, provision, t])

  const onSave = async () => {
    setErrorMsg(null)
    try {
      const config = await buildConfigWithSecretRefs(draft, secretInputs, async (body) => {
        const secret = await createSecretMut.mutateAsync({ body })
        return secret.id
      })
      setDraft(config)
      setSecretInputs(emptySecretInputs())
      const change = await mut.mutateAsync({ agentID, config })
      setDraft(change.new)
      onToast(t("agents.feishuConnector.saved"))
    } catch (err) {
      if (err instanceof ApiError) {
        // api-client copies the JSON `error` field into envelope.code,
        // so the discriminator string lives there (not in message).
        const code = err.envelope.code
        if (code === "feishu_app_id_in_use") {
          setErrorMsg(t("agents.feishuConnector.errors.appIdInUse"))
          return
        }
        if (code === "feishu_connector_incomplete") {
          setErrorMsg(t("agents.feishuConnector.errors.incomplete"))
          return
        }
      }
      setErrorMsg(err instanceof Error ? err.message : t("agents.feishuConnector.errors.generic"))
    }
  }

  const onReset = () => {
    setDraft(current ?? EMPTY_CONFIG)
    setSecretInputs(emptySecretInputs())
    setErrorMsg(null)
  }

  const onBeginProvision = () => {
    setErrorMsg(null)
    beginProvisionMut.mutate(agentID, {
      onSuccess: async (res) => {
        const begin = res.begin
        if (!begin?.device_code || !begin.verification_uri_complete) {
          setErrorMsg(t("agents.feishuConnector.provision.failed"))
          return
        }
        try {
          const qrDataUrl = await QRCode.toDataURL(begin.verification_uri_complete, {
            width: 224,
            margin: 2,
            color: { dark: "#020617", light: "#ffffff" },
          })
          setProvision({
            deviceCode: begin.device_code,
            userCode: begin.user_code,
            verificationUrl: begin.verification_uri_complete,
            qrDataUrl,
            expiresAt: Date.now() + Math.max(30, begin.expires_in) * 1000,
            intervalSec: begin.interval || 5,
            status: "pending",
          })
        } catch (err) {
          setErrorMsg(err instanceof Error ? err.message : t("agents.feishuConnector.provision.failed"))
        }
      },
      onError: (err) => {
        setErrorMsg(err instanceof ApiError ? err.envelope.message : t("agents.feishuConnector.provision.failed"))
      },
    })
  }

  return (
    <Card
      title={t("agents.feishuConnector.title")}
      description={t("agents.feishuConnector.description")}
      className="mt-4"
    >
      {draft.enabled && (
        <div className="mb-4 rounded-md border border-line bg-surface p-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <QrCode className="h-4 w-4 text-fg-muted" strokeWidth={1.75} />
              <div>
                <p className="text-sm font-medium text-fg">
                  {t("agents.feishuConnector.provision.title")}
                </p>
                <p className="text-sm text-fg-subtle">
                  {t("agents.feishuConnector.provision.subtitle")}
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={onBeginProvision}
              disabled={!canEdit || saving || beginProvisionMut.isPending || provision?.status === "pending"}
              className="inline-flex items-center gap-2 rounded-md bg-surface-emphasis px-3 py-1.5 text-sm font-medium text-white hover:bg-surface-emphasis disabled:opacity-60"
              data-testid="feishu-provision-begin-button"
            >
              {beginProvisionMut.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <QrCode className="h-3.5 w-3.5" />}
              {t("agents.feishuConnector.provision.start")}
            </button>
          </div>

          {provision && (
            <div className="mt-3 grid gap-3 sm:grid-cols-[auto_1fr]">
              {provision.qrDataUrl && provision.status === "pending" && (
                <img
                  src={provision.qrDataUrl}
                  alt={t("agents.feishuConnector.provision.qrAlt")}
                  className="h-40 w-40 rounded-md border border-line bg-surface p-2"
                  data-testid="feishu-provision-qr"
                />
              )}
              <div className="min-w-0 space-y-2 text-sm text-fg-muted">
                <ProvisionStatusIcon status={provision.status} loading={pollProvisionPending} />
                <p className="font-mono text-sm text-fg-emphasis">{provision.userCode}</p>
                <a
                  href={provision.verificationUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex max-w-full items-center gap-1 text-sm text-fg-muted underline underline-offset-2"
                >
                  <span className="truncate">{t("agents.feishuConnector.provision.openLink")}</span>
                  <ExternalLink className="h-3.5 w-3.5 shrink-0" />
                </a>
                {provision.status === "pending" && (
                  <p className="inline-flex items-center gap-1 text-fg-subtle">
                    {pollProvisionPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                    {t("agents.feishuConnector.provision.pending")}
                  </p>
                )}
                {provision.message && (
                  <p className={provision.status === "success" ? "text-success" : "text-danger"}>
                    {provision.message}
                  </p>
                )}
              </div>
            </div>
          )}
        </div>
      )}

      {SHOW_FEISHU_DIAGNOSTICS && (
        <FeishuDiagnosticsStrip
          diagnostics={undefined}
          loading={false}
          hasError={false}
          formatTime={() => ""}
        />
      )}

      <Field
        label={t("agents.feishuConnector.fields.entryMode.label")}
        hint={t("agents.feishuConnector.fields.entryMode.hint")}
      >
        <div className="grid grid-cols-2 gap-2" data-testid="feishu-entry-mode-control">
          {(["default", "dedicated"] as const).map((mode) => {
            const active = entryMode === mode
            return (
              <button
                key={mode}
                type="button"
                onClick={() => onEntryModeChange(mode)}
                disabled={!canEdit || saving}
                className={`min-h-9 rounded-md border px-3 py-1.5 text-sm font-medium transition ${
                  active
                    ? "border-line-strong bg-surface-emphasis text-white"
                    : "border-line bg-surface text-fg-muted hover:bg-surface-subtle"
                } disabled:opacity-60`}
                aria-pressed={active}
              >
                {t(`agents.feishuConnector.fields.entryMode.options.${mode}`)}
              </button>
            )
          })}
        </div>
      </Field>

      {draft.enabled && (
        <>
          <Field
            label={t("agents.feishuConnector.fields.appId.label")}
            hint={t("agents.feishuConnector.fields.appId.hint")}
            required
          >
            <input
              type="text"
              value={draft.app_id}
              placeholder="cli_xxxxxxxxxxxxxxxx"
              onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
              disabled={!canEdit || saving}
              className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
              data-testid="feishu-app-id-input"
            />
          </Field>

          <SecretInput
            label={t("agents.feishuConnector.fields.appSecret.label")}
            hint={t("agents.feishuConnector.fields.appSecret.hint")}
            savedHint={t("agents.feishuConnector.fields.appSecret.savedHint")}
            value={secretInputs.appSecret}
            onChange={(v) => setSecretInputs((prev) => ({ ...prev, appSecret: v }))}
            required={!draft.app_secret_ref.trim()}
            hasSavedValue={Boolean(draft.app_secret_ref.trim())}
            disabled={!canEdit || saving}
            testId="feishu-app-secret-input"
          />

          <SecretInput
            label={t("agents.feishuConnector.fields.verificationToken.label")}
            hint={t("agents.feishuConnector.fields.verificationToken.hint")}
            savedHint={t("agents.feishuConnector.fields.verificationToken.savedHint")}
            value={secretInputs.verificationToken}
            onChange={(v) => setSecretInputs((prev) => ({ ...prev, verificationToken: v }))}
            required={draft.event_mode !== "websocket" && !draft.verification_token_ref.trim()}
            hasSavedValue={Boolean(draft.verification_token_ref.trim())}
            disabled={!canEdit || saving}
            testId="feishu-verification-token-input"
          />

          <SecretInput
            label={t("agents.feishuConnector.fields.encryptKey.label")}
            hint={t("agents.feishuConnector.fields.encryptKey.hint")}
            savedHint={t("agents.feishuConnector.fields.encryptKey.savedHint")}
            value={secretInputs.encryptKey}
            onChange={(v) => setSecretInputs((prev) => ({ ...prev, encryptKey: v }))}
            required={false}
            hasSavedValue={Boolean(draft.encrypt_key_ref.trim())}
            disabled={!canEdit || saving}
            testId="feishu-encrypt-key-input"
          />

          <Field
            label={t("agents.feishuConnector.fields.botOpenId.label")}
            hint={t("agents.feishuConnector.fields.botOpenId.hint")}
          >
            <input
              type="text"
              value={draft.bot_open_id}
              placeholder="ou_xxxxxxxxxxxxxxxx"
              onChange={(e) => setDraft({ ...draft, bot_open_id: e.target.value })}
              disabled={!canEdit || saving}
              className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
              data-testid="feishu-bot-open-id-input"
            />
          </Field>
        </>
      )}

      {!canEdit && (
        <p className="mt-3 text-sm text-fg-faint">{t("agents.feishuConnector.ownerOnly")}</p>
      )}

      {errorMsg && (
        <p className="mt-3 text-sm text-danger" role="alert" data-testid="feishu-error">
          {errorMsg}
        </p>
      )}

      <div className="mt-4 flex items-center justify-end gap-2">
        {dirty && (
          <button
            type="button"
            onClick={onReset}
            disabled={saving}
            className="inline-flex items-center gap-2 rounded-md border border-line px-3 py-1.5 text-sm text-fg-muted hover:bg-surface-subtle disabled:opacity-60"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            {t("agents.feishuConnector.actions.reset")}
          </button>
        )}
        <button
          type="button"
          onClick={onSave}
          disabled={!canEdit || saving || !dirty || Boolean(missingRequired)}
          className="inline-flex items-center gap-2 rounded-md bg-surface-emphasis px-3 py-1.5 text-sm font-medium text-white hover:bg-surface-emphasis disabled:opacity-60"
          data-testid="feishu-save-button"
        >
          {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {t("agents.feishuConnector.actions.save")}
        </button>
      </div>
    </Card>
  )
}

type FeishuDiagnosticsStatus =
  | "loading"
  | "unreachable"
  | "notConfigured"
  | "disabled"
  | "ready"
  | "inboundOnly"
  | "pending"
  | "retrying"
  | "error"

function FeishuDiagnosticsStrip({
  diagnostics,
  loading,
  hasError,
  formatTime,
}: {
  diagnostics: FeishuConnectorDiagnostics | undefined
  loading: boolean
  hasError: boolean
  formatTime: (iso: string | null | undefined) => string
}) {
  const { t } = useTranslation("admin")
  const status = resolveFeishuDiagnosticsStatus(diagnostics, loading, hasError)
  const emptyValue = loading && !diagnostics ? "..." : t("agents.feishuConnector.diagnostics.empty")
  const mode = diagnostics?.configured
    ? t(`agents.feishuConnector.diagnostics.mode.${diagnostics.event_mode}`)
    : emptyValue
  const counts = [
    ["inbound", diagnostics?.inbound_message_count],
    ["outbound", diagnostics?.outbound_message_count],
    ["delivered", diagnostics?.delivered_outbound_count],
    ["pending", diagnostics?.pending_outbound_count],
    ["retrying", diagnostics?.retrying_outbound_count],
    ["dead", diagnostics?.dead_outbound_count],
  ] as const
  const times = [
    ["inbound", diagnostics?.last_inbound_at],
    ["outbound", diagnostics?.last_outbound_at],
    ["delivered", diagnostics?.last_delivered_at],
  ] as const

  return (
    <div
      className="mb-3 rounded-md border border-line bg-surface-subtle p-3"
      data-testid="feishu-diagnostics-strip"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <FeishuDiagnosticsBadge status={status} />
        <span className="rounded-md bg-surface px-2 py-1 font-mono text-xs text-fg-subtle ring-1 ring-slate-200">
          {mode}
        </span>
      </div>

      <div className="mt-3 grid grid-cols-1 gap-2 min-[520px]:grid-cols-3 sm:grid-cols-6">
        {counts.map(([key, value]) => (
          <div key={key} className="min-w-0 rounded-md bg-surface px-2 py-2 ring-1 ring-slate-200">
            <p className="truncate text-xs uppercase tracking-wider text-fg-faint">
              {t(`agents.feishuConnector.diagnostics.stats.${key}`)}
            </p>
            <p className="mt-0.5 truncate font-mono text-sm font-semibold tabular-nums text-fg-emphasis">
              {typeof value === "number" ? value : emptyValue}
            </p>
          </div>
        ))}
      </div>

      <div className="mt-2 grid grid-cols-1 gap-2 border-t border-line pt-2 min-[520px]:grid-cols-3">
        {times.map(([key, value]) => (
          <FeishuDiagnosticTime
            key={key}
            label={t(`agents.feishuConnector.diagnostics.times.${key}`)}
            value={diagnostics ? formatTime(value) : emptyValue}
          />
        ))}
      </div>

      {diagnostics?.last_error && (
        <p className="mt-2 flex items-start gap-1.5 break-words text-sm text-danger-emphasis">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>
            {t("agents.feishuConnector.diagnostics.lastError")}: {diagnostics.last_error}
          </span>
        </p>
      )}
    </div>
  )
}

function FeishuDiagnosticsBadge({ status }: { status: FeishuDiagnosticsStatus }) {
  const { t } = useTranslation("admin")
  const label = t(`agents.feishuConnector.diagnostics.status.${status}`)
  const warningStatus = status === "pending" || status === "retrying" || status === "inboundOnly"
  const tone =
    status === "ready"
      ? "bg-success-subtle text-success ring-emerald-200"
      : status === "error" || status === "unreachable"
        ? "bg-danger-subtle text-danger-emphasis ring-rose-200"
        : warningStatus
          ? "bg-warning-subtle text-warning ring-amber-200"
          : "bg-surface text-fg-muted ring-slate-200"
  const Icon =
    status === "loading"
      ? Loader2
      : status === "ready"
        ? CheckCircle2
        : status === "error" || status === "unreachable"
          ? XCircle
          : warningStatus
            ? AlertTriangle
            : MessageCircle

  return (
    <span className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm font-medium ring-1 ${tone}`}>
      <Icon className={`h-3.5 w-3.5 ${status === "loading" ? "animate-spin" : ""}`} />
      {label}
    </span>
  )
}

function FeishuDiagnosticTime({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="text-xs uppercase tracking-wider text-fg-faint">{label}</p>
      <p className="mt-0.5 truncate text-sm text-fg-muted" title={value}>
        {value}
      </p>
    </div>
  )
}

function resolveFeishuDiagnosticsStatus(
  diagnostics: FeishuConnectorDiagnostics | undefined,
  loading: boolean,
  hasError: boolean,
): FeishuDiagnosticsStatus {
  if (!diagnostics && loading) return "loading"
  if (!diagnostics && hasError) return "unreachable"
  if (!diagnostics?.configured) return "notConfigured"
  if (!diagnostics.enabled) return "disabled"
  if (diagnostics.dead_outbound_count > 0) return "error"
  if (diagnostics.retrying_outbound_count > 0) return "retrying"
  if (diagnostics.pending_outbound_count > 0) return "pending"
  if (diagnostics.inbound_message_count > 0 && diagnostics.outbound_message_count === 0) return "inboundOnly"
  return "ready"
}

function SecretInput({
  label,
  hint,
  savedHint,
  value,
  onChange,
  required,
  hasSavedValue,
  disabled,
  testId,
}: {
  label: string
  hint: string
  savedHint: string
  value: string
  onChange: (v: string) => void
  required: boolean
  hasSavedValue: boolean
  disabled: boolean
  testId: string
}) {
  return (
    <Field label={label} hint={hasSavedValue ? savedHint : hint} required={required}>
      <input
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        autoComplete="new-password"
        className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
        data-testid={testId}
      />
    </Field>
  )
}

function ProvisionStatusIcon({
  status,
  loading,
}: {
  status: ProvisionState["status"]
  loading: boolean
}) {
  const { t } = useTranslation("admin")
  if (status === "success") {
    return (
      <p className="inline-flex items-center gap-1 text-success">
        <CheckCircle2 className="h-3.5 w-3.5" />
        <span>{t("agents.feishuConnector.provision.status.connected")}</span>
      </p>
    )
  }
  if (status === "error" || status === "expired") {
    return (
      <p className="inline-flex items-center gap-1 text-danger">
        <XCircle className="h-3.5 w-3.5" />
        <span>{t("agents.feishuConnector.provision.status.stopped")}</span>
      </p>
    )
  }
  return (
    <p className="inline-flex items-center gap-1 text-fg-subtle">
      {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <QrCode className="h-3.5 w-3.5" />}
      <span>{t("agents.feishuConnector.provision.status.waiting")}</span>
    </p>
  )
}

function emptySecretInputs(): SecretInputs {
  return { ...EMPTY_SECRET_INPUTS }
}

function secretInputsDirty(inputs: SecretInputs): boolean {
  return Boolean(inputs.appSecret.trim() || inputs.verificationToken.trim() || inputs.encryptKey.trim())
}

async function buildConfigWithSecretRefs(
  draft: FeishuConnectorConfig,
  inputs: SecretInputs,
  createSecret: (body: CreateSecretRequest) => Promise<string>,
): Promise<FeishuConnectorConfig> {
  const next = trimConfig(draft)
  if (!next.enabled) return next

  for (const field of Object.keys(FEISHU_SECRET_FIELDS) as FeishuSecretField[]) {
    const plaintext = inputs[field].trim()
    if (!plaintext) continue
    const spec = FEISHU_SECRET_FIELDS[field]
    next[spec.refKey] = await createSecret(createFeishuSecretBody(spec, plaintext))
  }

  return next
}

function trimConfig(config: FeishuConnectorConfig): FeishuConnectorConfig {
  return {
    enabled: config.enabled,
    app_id: config.app_id.trim(),
    app_secret_ref: config.app_secret_ref.trim(),
    verification_token_ref: config.verification_token_ref.trim(),
    encrypt_key_ref: config.encrypt_key_ref.trim(),
    bot_open_id: config.bot_open_id.trim(),
    event_mode: config.event_mode,
    routing_mode: config.routing_mode,
  }
}

function createFeishuSecretBody(spec: FeishuSecretFieldSpec, plaintext: string): CreateSecretRequest {
  return {
    name: spec.namePrefix + "-" + randomHex(6),
    kind: spec.kind,
    provider: "feishu",
    auth_type: spec.authType,
    payload: { [spec.payloadKey]: plaintext },
  }
}

function randomHex(bytes: number): string {
  const values = new Uint8Array(bytes)
  crypto.getRandomValues(values)
  return Array.from(values, (value) => value.toString(16).padStart(2, "0")).join("")
}

function configEqual(a: FeishuConnectorConfig, b: FeishuConnectorConfig): boolean {
  return (
    a.enabled === b.enabled &&
    a.app_id === b.app_id &&
    a.app_secret_ref === b.app_secret_ref &&
    a.verification_token_ref === b.verification_token_ref &&
    a.encrypt_key_ref === b.encrypt_key_ref &&
    a.bot_open_id === b.bot_open_id &&
    a.event_mode === b.event_mode &&
    a.routing_mode === b.routing_mode
  )
}

/** Extract the current Feishu connector config from an Agent's
 *  config jsonb; undefined when never wired. */
export function readFeishuConfigFromAgent(
  agentConfig: Record<string, unknown> | undefined,
): FeishuConnectorConfig | undefined {
  if (!agentConfig) return undefined
  const connectors = agentConfig["connectors"] as Record<string, unknown> | undefined
  const feishu = connectors?.["feishu"] as Record<string, unknown> | undefined
  if (!feishu) return undefined
  return {
    enabled: Boolean(feishu["enabled"]),
    app_id: (feishu["app_id"] as string) ?? "",
    app_secret_ref: (feishu["app_secret_ref"] as string) ?? "",
    verification_token_ref: (feishu["verification_token_ref"] as string) ?? "",
    encrypt_key_ref: (feishu["encrypt_key_ref"] as string) ?? "",
    bot_open_id: (feishu["bot_open_id"] as string) ?? "",
    event_mode: feishu["event_mode"] === "websocket" ? "websocket" : "webhook",
    routing_mode: feishu["routing_mode"] === "shared" ? "shared" : "direct",
  }
}
