import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2, RefreshCw } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useUpdateWorkspaceFeishuConnector,
  type FeishuConnectorInput,
} from "../../../lib/api-connectors"
import { useCreateSecret } from "../../../lib/api-secrets"
import type { CreateSecretRequest } from "../../../lib/api-types"
import { Card, Field, SecretInput, randomHex } from "./shared"

const EMPTY_CONFIG: FeishuConnectorInput = {
  enabled: false,
  app_id: "",
  app_secret_ref: "",
  verification_token_ref: "",
  encrypt_key_ref: "",
  bot_open_id: "",
  event_mode: "websocket",
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

export interface FeishuConnectorFieldsProps {
  workspaceID: string | null
  current: FeishuConnectorInput | undefined
  canEdit: boolean
  onToast: (msg: string) => void
}

export function FeishuConnectorFields({
  workspaceID,
  current,
  canEdit,
  onToast,
}: FeishuConnectorFieldsProps) {
  const currentConfig = current ?? EMPTY_CONFIG
  return (
    <FeishuConnectorFieldsInner
      key={configKey(currentConfig)}
      workspaceID={workspaceID}
      current={currentConfig}
      canEdit={canEdit}
      onToast={onToast}
    />
  )
}

type FeishuConnectorFieldsInnerProps = Omit<FeishuConnectorFieldsProps, "current"> & {
  current: FeishuConnectorInput
}

function FeishuConnectorFieldsInner({
  workspaceID,
  current,
  canEdit,
  onToast,
}: FeishuConnectorFieldsInnerProps) {
  const { t } = useTranslation("admin")
  const mut = useUpdateWorkspaceFeishuConnector(workspaceID)
  const createSecretMut = useCreateSecret(workspaceID)

  const [draft, setDraft] = useState<FeishuConnectorInput>(current)
  const [secretInputs, setSecretInputs] = useState<SecretInputs>({ ...EMPTY_SECRET_INPUTS })
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const dirty = !configEqual(draft, current) || secretInputsDirty(secretInputs)
  const saving = mut.isPending || createSecretMut.isPending

  // bot_open_id is intentionally NOT required: when left blank the server
  // derives it from the app credentials (bot/v3/info) at save time.
  const missingRequired = missingRequiredFor(draft, secretInputs)
  const missingRequiredToEnable = missingRequiredFor({ ...draft, enabled: true }, secretInputs)
  const missingDraftIdentity = !draft.app_id.trim()

  const onSave = async (nextEnabled = draft.enabled) => {
    const nextDraft = { ...draft, enabled: nextEnabled }
    if (missingRequiredFor(nextDraft, secretInputs)) {
      setErrorMsg(t("connections.connector.feishu.errors.incomplete"))
      return
    }
    setErrorMsg(null)
    try {
      const config = await buildConfigWithSecretRefs(nextDraft, secretInputs, async (body) => {
        const secret = await createSecretMut.mutateAsync({ body })
        return secret.id
      })
      setDraft(config)
      setSecretInputs({ ...EMPTY_SECRET_INPUTS })
      const change = await mut.mutateAsync({ config })
      applyChange(setDraft, config, change.config)
      onToast(t("connections.connector.feishu.saved"))
    } catch (err) {
      if (err instanceof ApiError) {
        const code = err.envelope.code
        if (code === "feishu_app_id_in_use") {
          setErrorMsg(t("connections.connector.feishu.errors.appIdInUse"))
          return
        }
        if (code === "feishu_connector_incomplete") {
          setErrorMsg(t("connections.connector.feishu.errors.incomplete"))
          return
        }
        if (code === "feishu_bot_open_id_resolve_failed") {
          setErrorMsg(t("connections.connector.feishu.errors.botOpenIdResolveFailed"))
          return
        }
      }
      setErrorMsg(
        err instanceof Error ? err.message : t("connections.connector.feishu.errors.generic"),
      )
    }
  }

  const onReset = () => {
    setDraft(current)
    setSecretInputs({ ...EMPTY_SECRET_INPUTS })
    setErrorMsg(null)
  }

  return (
    <Card
      title={t("connections.connector.feishu.title")}
      description={t("connections.connector.feishu.description")}
      docHref={t("connections.connector.feishu.docLink.href")}
      docLabel={t("connections.connector.feishu.docLink.label")}
    >
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-line bg-surface-subtle px-3 py-2">
        <div className="min-w-0">
          <p className="text-sm font-medium text-fg">
            {draft.enabled
              ? t("connections.connector.status.enabled")
              : t("connections.connector.status.disabled")}
          </p>
          <p className="mt-0.5 text-xs text-fg-subtle">
            {t("connections.connector.feishu.fields.enabled.hint")}
          </p>
        </div>
        <label className="inline-flex items-center gap-2 text-sm text-fg">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            disabled={!canEdit || saving}
            data-testid="feishu-enabled-input"
          />
          {t("connections.connector.feishu.fields.enabled.toggle")}
        </label>
      </div>

      <Field
        label={t("connections.connector.feishu.fields.appId.label")}
        hint={t("connections.connector.feishu.fields.appId.hint")}
        required
      >
        <input
          type="text"
          value={draft.app_id}
          placeholder="cli_xxxxxxxxxxxxxxxx"
          onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
          disabled={!canEdit || saving}
          className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong disabled:bg-surface-subtle"
          data-testid="feishu-app-id-input"
        />
      </Field>

      <SecretInput
        label={t("connections.connector.feishu.fields.appSecret.label")}
        hint={t("connections.connector.feishu.fields.appSecret.hint")}
        savedHint={t("connections.connector.feishu.fields.appSecret.savedHint")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.appSecret}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, appSecret: v }))}
        required={!draft.app_secret_ref.trim()}
        hasSavedValue={Boolean(draft.app_secret_ref.trim())}
        disabled={!canEdit || saving}
        testId="feishu-app-secret-input"
      />

      <SecretInput
        label={t("connections.connector.feishu.fields.verificationToken.label")}
        hint={t("connections.connector.feishu.fields.verificationToken.hint")}
        savedHint={t("connections.connector.feishu.fields.verificationToken.savedHint")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.verificationToken}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, verificationToken: v }))}
        required={draft.event_mode === "webhook" && !draft.verification_token_ref.trim()}
        hasSavedValue={Boolean(draft.verification_token_ref.trim())}
        disabled={!canEdit || saving}
        testId="feishu-verification-token-input"
      />

      <SecretInput
        label={t("connections.connector.feishu.fields.encryptKey.label")}
        hint={t("connections.connector.feishu.fields.encryptKey.hint")}
        savedHint={t("connections.connector.feishu.fields.encryptKey.savedHint")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.encryptKey}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, encryptKey: v }))}
        required={false}
        hasSavedValue={Boolean(draft.encrypt_key_ref.trim())}
        disabled={!canEdit || saving}
        testId="feishu-encrypt-key-input"
      />

      <Field
        label={t("connections.connector.feishu.fields.botOpenId.label")}
        hint={t("connections.connector.feishu.fields.botOpenId.hint")}
      >
        <input
          type="text"
          value={draft.bot_open_id}
          placeholder={t("connections.connector.feishu.fields.botOpenId.placeholder")}
          onChange={(e) => setDraft({ ...draft, bot_open_id: e.target.value })}
          disabled={!canEdit || saving}
          className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong disabled:bg-surface-subtle"
          data-testid="feishu-bot-open-id-input"
        />
      </Field>

      <Field
        label={t("connections.connector.feishu.fields.eventMode.label")}
        hint={t("connections.connector.feishu.fields.eventMode.hint")}
      >
        <div className="grid grid-cols-2 gap-2">
          {(["websocket", "webhook"] as const).map((mode) => {
            const active = draft.event_mode === mode
            return (
              <button
                key={mode}
                type="button"
                onClick={() => setDraft({ ...draft, event_mode: mode })}
                disabled={!canEdit || saving}
                className={`min-h-9 rounded-md border px-3 py-1.5 text-sm font-medium transition ${
                  active
                    ? "border-line-strong bg-surface-emphasis text-white"
                    : "border-line bg-surface text-fg-muted hover:bg-surface-subtle"
                } disabled:opacity-60`}
                aria-pressed={active}
              >
                {t(`connections.connector.feishu.fields.eventMode.options.${mode}`)}
              </button>
            )
          })}
        </div>
      </Field>

      {!canEdit && (
        <p className="mt-3 text-sm text-fg-faint">{t("connections.connector.adminOnly")}</p>
      )}

      {errorMsg && (
        <p className="mt-3 text-sm text-danger" role="alert" data-testid="feishu-error">
          {errorMsg}
        </p>
      )}

      <div className="mt-5 flex flex-wrap items-center justify-end gap-2 border-t border-line/40 pt-4">
        {dirty && (
          <button
            type="button"
            onClick={onReset}
            disabled={saving}
            className="inline-flex items-center gap-2 rounded-md border border-line px-3 py-1.5 text-sm text-fg-muted hover:bg-surface-subtle disabled:opacity-60"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            {t("connections.connector.actions.reset")}
          </button>
        )}
        <button
          type="button"
          onClick={() => void onSave()}
          disabled={
            !canEdit || saving || !dirty || missingDraftIdentity || Boolean(missingRequired)
          }
          className="inline-flex items-center gap-2 rounded-md border border-line px-3 py-1.5 text-sm font-medium text-fg-muted hover:bg-surface-subtle disabled:opacity-60"
          data-testid="feishu-save-button"
        >
          {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {draft.enabled
            ? t("connections.connector.actions.save")
            : t("connections.connector.actions.saveDraft")}
        </button>
        {!draft.enabled && (
          <button
            type="button"
            onClick={() => void onSave(true)}
            disabled={!canEdit || saving || Boolean(missingRequiredToEnable)}
            className="inline-flex items-center gap-2 rounded-md bg-surface-emphasis px-3 py-1.5 text-sm font-medium text-white hover:bg-surface-emphasis disabled:opacity-60"
            data-testid="feishu-save-enable-button"
          >
            {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("connections.connector.actions.saveAndEnable")}
          </button>
        )}
      </div>
    </Card>
  )
}

function missingRequiredFor(draft: FeishuConnectorInput, secretInputs: SecretInputs): boolean {
  return (
    draft.enabled &&
    (!draft.app_id.trim() ||
      (!draft.app_secret_ref.trim() && !secretInputs.appSecret.trim()) ||
      (draft.event_mode === "webhook" &&
        !draft.verification_token_ref.trim() &&
        !secretInputs.verificationToken.trim()))
  )
}

function configKey(config: FeishuConnectorInput): string {
  return [
    config.enabled ? "1" : "0",
    config.app_id,
    config.app_secret_ref,
    config.verification_token_ref,
    config.encrypt_key_ref,
    config.bot_open_id,
    config.event_mode,
  ].join("\u0000")
}

function applyChange(
  setDraft: (c: FeishuConnectorInput) => void,
  sent: FeishuConnectorInput,
  config: Record<string, unknown>,
) {
  const str = (k: string) => (typeof config[k] === "string" ? (config[k] as string) : "")
  const mode = str("event_mode")
  setDraft({
    enabled: sent.enabled,
    app_id: sent.app_id,
    app_secret_ref: str("app_secret_ref"),
    verification_token_ref: str("verification_token_ref"),
    encrypt_key_ref: str("encrypt_key_ref"),
    bot_open_id: str("bot_open_id"),
    event_mode: mode === "webhook" ? "webhook" : "websocket",
  })
}

function secretInputsDirty(inputs: SecretInputs): boolean {
  return Boolean(
    inputs.appSecret.trim() || inputs.verificationToken.trim() || inputs.encryptKey.trim(),
  )
}

async function buildConfigWithSecretRefs(
  draft: FeishuConnectorInput,
  inputs: SecretInputs,
  createSecret: (body: CreateSecretRequest) => Promise<string>,
): Promise<FeishuConnectorInput> {
  const next = trimConfig(draft)

  for (const field of Object.keys(FEISHU_SECRET_FIELDS) as FeishuSecretField[]) {
    const plaintext = inputs[field].trim()
    if (!plaintext) continue
    const spec = FEISHU_SECRET_FIELDS[field]
    next[spec.refKey] = await createSecret(createFeishuSecretBody(spec, plaintext))
  }

  return next
}

function trimConfig(config: FeishuConnectorInput): FeishuConnectorInput {
  return {
    enabled: config.enabled,
    app_id: config.app_id.trim(),
    app_secret_ref: config.app_secret_ref.trim(),
    verification_token_ref: config.verification_token_ref.trim(),
    encrypt_key_ref: config.encrypt_key_ref.trim(),
    bot_open_id: config.bot_open_id.trim(),
    event_mode: config.event_mode === "webhook" ? "webhook" : "websocket",
  }
}

function createFeishuSecretBody(
  spec: FeishuSecretFieldSpec,
  plaintext: string,
): CreateSecretRequest {
  return {
    name: spec.namePrefix + "-" + randomHex(6),
    kind: spec.kind,
    provider: "feishu",
    auth_type: spec.authType,
    payload: { [spec.payloadKey]: plaintext },
  }
}

function configEqual(a: FeishuConnectorInput, b: FeishuConnectorInput): boolean {
  return (
    a.enabled === b.enabled &&
    a.app_id === b.app_id &&
    a.app_secret_ref === b.app_secret_ref &&
    a.verification_token_ref === b.verification_token_ref &&
    a.encrypt_key_ref === b.encrypt_key_ref &&
    a.bot_open_id === b.bot_open_id &&
    a.event_mode === b.event_mode
  )
}
