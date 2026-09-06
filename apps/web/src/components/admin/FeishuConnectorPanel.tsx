import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import QRCode from "qrcode"
import { ExternalLink, Loader2, QrCode } from "lucide-react"

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
import { randomHex } from "../../lib/random"
import { Badge } from "../ui/badge"
import { Button } from "../ui/button"
import { Field } from "../ui/label"
import { Input } from "../ui/input"
import { PropertyList, Property } from "../ui/property-list"
import { StatusIcon, type StatusKind } from "../ui/status-icon"
import { Tabs, TabsList, TabsTrigger } from "../ui/tabs"
import { InlineError } from "../../pages/admin/agents/DetailSection"

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

function RequiredMark() {
  return <span aria-hidden="true"> *</span>
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
            color: { dark: "#37352f", light: "#ffffff" },
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

  const disabled = !canEdit || saving

  return (
    <section className="mt-6 max-w-2xl">
      <div className="mb-2 flex h-7 items-center justify-between gap-2">
        <h2 className="text-sm font-medium text-fg">{t("agents.feishuConnector.title")}</h2>
      </div>

      <div className="flex flex-col gap-3">
        <Field
          label={t("agents.feishuConnector.fields.entryMode.label")}
          hint={t("agents.feishuConnector.fields.entryMode.hint")}
        >
          <Tabs
            value={entryMode}
            onValueChange={(mode) => onEntryModeChange(mode === "dedicated" ? "dedicated" : "default")}
          >
            <TabsList className="flex w-full" data-testid="feishu-entry-mode-control">
              {(["default", "dedicated"] as const).map((mode) => (
                <TabsTrigger key={mode} value={mode} className="flex-1" disabled={disabled}>
                  {t(`agents.feishuConnector.fields.entryMode.options.${mode}`)}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        </Field>

        {draft.enabled && (
          <>
            <div className="mt-3 border-t border-line pt-3">
              <div className="flex h-7 items-center justify-between gap-2">
                <h3 className="text-sm font-medium text-fg">{t("agents.feishuConnector.provision.title")}</h3>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={onBeginProvision}
                  disabled={disabled || beginProvisionMut.isPending || provision?.status === "pending"}
                  data-testid="feishu-provision-begin-button"
                >
                  {beginProvisionMut.isPending ? (
                    <Loader2 className="animate-spin" strokeWidth={1.5} aria-hidden="true" />
                  ) : (
                    <QrCode strokeWidth={1.5} aria-hidden="true" />
                  )}
                  {t("agents.feishuConnector.provision.start")}
                </Button>
              </div>

              {provision && (
                <div className="mt-2 flex items-start gap-4">
                  {provision.qrDataUrl && provision.status === "pending" && (
                    <img
                      src={provision.qrDataUrl}
                      alt={t("agents.feishuConnector.provision.qrAlt")}
                      className="h-36 w-36 shrink-0 rounded-md border border-line"
                      data-testid="feishu-provision-qr"
                    />
                  )}
                  <div className="flex min-w-0 flex-1 flex-col items-start gap-1.5">
                    <ProvisionStatus status={provision.status} loading={pollProvisionPending} />
                    <code className="font-mono text-xs text-fg">{provision.userCode}</code>
                    <Button variant="link" size="sm" className="px-0" asChild>
                      <a href={provision.verificationUrl} target="_blank" rel="noreferrer">
                        <span className="truncate">{t("agents.feishuConnector.provision.openLink")}</span>
                        <ExternalLink strokeWidth={1.5} aria-hidden="true" />
                      </a>
                    </Button>
                    {provision.message && <p className="text-sm text-fg">{provision.message}</p>}
                  </div>
                </div>
              )}
            </div>

            {SHOW_FEISHU_DIAGNOSTICS && (
              <FeishuDiagnosticsStrip
                diagnostics={undefined}
                loading={false}
                hasError={false}
                formatTime={() => ""}
              />
            )}

            <div className="mt-3 flex flex-col gap-3 border-t border-line pt-3">
              <Field
                label={<>{t("agents.feishuConnector.fields.appId.label")}<RequiredMark /></>}
                hint={t("agents.feishuConnector.fields.appId.hint")}
                htmlFor="feishu-app-id"
              >
                <Input
                  id="feishu-app-id"
                  type="text"
                  value={draft.app_id}
                  placeholder="cli_xxxxxxxxxxxxxxxx"
                  onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
                  disabled={disabled}
                  className="font-mono"
                  data-testid="feishu-app-id-input"
                />
              </Field>

              <SecretInput
                id="feishu-app-secret"
                label={t("agents.feishuConnector.fields.appSecret.label")}
                hint={t("agents.feishuConnector.fields.appSecret.hint")}
                savedHint={t("agents.feishuConnector.fields.appSecret.savedHint")}
                value={secretInputs.appSecret}
                onChange={(v) => setSecretInputs((prev) => ({ ...prev, appSecret: v }))}
                required={!draft.app_secret_ref.trim()}
                hasSavedValue={Boolean(draft.app_secret_ref.trim())}
                disabled={disabled}
                testId="feishu-app-secret-input"
              />

              <SecretInput
                id="feishu-verification-token"
                label={t("agents.feishuConnector.fields.verificationToken.label")}
                hint={t("agents.feishuConnector.fields.verificationToken.hint")}
                savedHint={t("agents.feishuConnector.fields.verificationToken.savedHint")}
                value={secretInputs.verificationToken}
                onChange={(v) => setSecretInputs((prev) => ({ ...prev, verificationToken: v }))}
                required={draft.event_mode !== "websocket" && !draft.verification_token_ref.trim()}
                hasSavedValue={Boolean(draft.verification_token_ref.trim())}
                disabled={disabled}
                testId="feishu-verification-token-input"
              />

              <SecretInput
                id="feishu-encrypt-key"
                label={t("agents.feishuConnector.fields.encryptKey.label")}
                hint={t("agents.feishuConnector.fields.encryptKey.hint")}
                savedHint={t("agents.feishuConnector.fields.encryptKey.savedHint")}
                value={secretInputs.encryptKey}
                onChange={(v) => setSecretInputs((prev) => ({ ...prev, encryptKey: v }))}
                required={false}
                hasSavedValue={Boolean(draft.encrypt_key_ref.trim())}
                disabled={disabled}
                testId="feishu-encrypt-key-input"
              />

              <Field
                label={t("agents.feishuConnector.fields.botOpenId.label")}
                hint={t("agents.feishuConnector.fields.botOpenId.hint")}
                htmlFor="feishu-bot-open-id"
              >
                <Input
                  id="feishu-bot-open-id"
                  type="text"
                  value={draft.bot_open_id}
                  placeholder="ou_xxxxxxxxxxxxxxxx"
                  onChange={(e) => setDraft({ ...draft, bot_open_id: e.target.value })}
                  disabled={disabled}
                  className="font-mono"
                  data-testid="feishu-bot-open-id-input"
                />
              </Field>
            </div>
          </>
        )}

        {!canEdit && (
          <p className="text-xs text-fg-muted">{t("agents.feishuConnector.ownerOnly")}</p>
        )}

        {errorMsg && (
          <InlineError role="alert" data-testid="feishu-error">{errorMsg}</InlineError>
        )}
      </div>

      <div className="mt-4 flex items-center justify-end gap-2 border-t border-line pt-3">
        <Button type="button" variant="outline" onClick={onReset} disabled={saving || !dirty}>
          {t("agents.feishuConnector.actions.reset")}
        </Button>
        <Button
          type="button"
          onClick={onSave}
          disabled={disabled || !dirty || Boolean(missingRequired)}
          data-testid="feishu-save-button"
        >
          {saving && <Loader2 className="animate-spin" strokeWidth={1.5} aria-hidden="true" />}
          {t("agents.feishuConnector.actions.save")}
        </Button>
      </div>
    </section>
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
  const emptyValue = loading && !diagnostics ? "…" : t("agents.feishuConnector.diagnostics.empty")
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
    <div className="mt-3 border-t border-line pt-3" data-testid="feishu-diagnostics-strip">
      <div className="flex h-7 items-center gap-2">
        <FeishuDiagnosticsBadge status={status} />
        <span className="font-mono text-xs text-fg-muted">{mode}</span>
      </div>

      <PropertyList className="mt-1">
        {counts.map(([key, value]) => (
          <Property key={key} label={t(`agents.feishuConnector.diagnostics.stats.${key}`)} mono className={typeof value === "number" ? undefined : "text-fg-muted"}>
            {typeof value === "number" ? value : emptyValue}
          </Property>
        ))}
        {times.map(([key, value]) => (
          <Property key={key} label={t(`agents.feishuConnector.diagnostics.times.${key}`)} mono>
            {diagnostics ? formatTime(value) : emptyValue}
          </Property>
        ))}
      </PropertyList>

      {diagnostics?.last_error && (
        <InlineError className="mt-2">
          {t("agents.feishuConnector.diagnostics.lastError")}: {diagnostics.last_error}
        </InlineError>
      )}
    </div>
  )
}

function FeishuDiagnosticsBadge({ status }: { status: FeishuDiagnosticsStatus }) {
  const { t } = useTranslation("admin")
  const label = t(`agents.feishuConnector.diagnostics.status.${status}`)
  const warningStatus = status === "pending" || status === "retrying" || status === "inboundOnly"
  const variant =
    status === "ready"
      ? "success"
      : status === "error" || status === "unreachable"
        ? "destructive"
        : warningStatus
          ? "warning"
          : "neutral"

  return (
    <Badge variant={variant} dot pulse={status === "loading"}>
      {label}
    </Badge>
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
  id,
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
  id: string
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
    <Field label={<>{label}{required && <RequiredMark />}</>} hint={hasSavedValue ? savedHint : hint} htmlFor={id}>
      <Input
        id={id}
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        autoComplete="new-password"
        className="font-mono"
        data-testid={testId}
      />
    </Field>
  )
}

/* The provisioning state as a status icon and an ink word; colour lives in the icon. */
function ProvisionStatus({
  status,
  loading,
}: {
  status: ProvisionState["status"]
  loading: boolean
}) {
  const { t } = useTranslation("admin")
  const kind: StatusKind = status === "success" ? "completed" : status === "error" || status === "expired" ? "failed" : loading ? "running" : "queued"
  const word = status === "success"
    ? t("agents.feishuConnector.provision.status.connected")
    : status === "error" || status === "expired"
      ? t("agents.feishuConnector.provision.status.stopped")
      : t("agents.feishuConnector.provision.status.waiting")
  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-fg">
      <StatusIcon status={kind} />
      <span>{word}</span>
      {status === "pending" && <span className="text-xs text-fg-muted">· {t("agents.feishuConnector.provision.pending")}</span>}
    </span>
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
