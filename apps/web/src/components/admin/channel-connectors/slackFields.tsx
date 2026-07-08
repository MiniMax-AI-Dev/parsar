import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2, RefreshCw } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useUpdateWorkspaceSlackConnector,
  type SlackConnectorInput,
} from "../../../lib/api-connectors"
import { useCreateSecret } from "../../../lib/api-secrets"
import type { CreateSecretRequest } from "../../../lib/api-types"
import { Card, Field, SecretInput, randomHex } from "./shared"

const EMPTY_CONFIG: SlackConnectorInput = {
  enabled: false,
  app_id: "",
  bot_token_ref: "",
  app_token_ref: "",
  signing_secret_ref: "",
  event_mode: "socket",
}

type SecretInputs = {
  botToken: string
  appToken: string
  signingSecret: string
}

type SlackSecretField = keyof SecretInputs
type SlackSecretRefKey = "bot_token_ref" | "app_token_ref" | "signing_secret_ref"

type SlackSecretFieldSpec = {
  refKey: SlackSecretRefKey
  kind: string
  authType: string
  payloadKey: string
  namePrefix: string
}

const EMPTY_SECRET_INPUTS: SecretInputs = {
  botToken: "",
  appToken: "",
  signingSecret: "",
}

const SLACK_SECRET_FIELDS: Record<SlackSecretField, SlackSecretFieldSpec> = {
  botToken: {
    refKey: "bot_token_ref",
    kind: "slack_bot_token",
    authType: "bot_token",
    payloadKey: "bot_token",
    namePrefix: "slack-bot-token",
  },
  appToken: {
    refKey: "app_token_ref",
    kind: "slack_app_token",
    authType: "app_token",
    payloadKey: "app_token",
    namePrefix: "slack-app-token",
  },
  signingSecret: {
    refKey: "signing_secret_ref",
    kind: "slack_signing_secret",
    authType: "signing_secret",
    payloadKey: "signing_secret",
    namePrefix: "slack-signing-secret",
  },
}

export interface SlackConnectorFieldsProps {
  workspaceID: string | null
  current: SlackConnectorInput | undefined
  canEdit: boolean
  onToast: (msg: string) => void
}

export function SlackConnectorFields({
  workspaceID,
  current,
  canEdit,
  onToast,
}: SlackConnectorFieldsProps) {
  const currentConfig = current ?? EMPTY_CONFIG
  return (
    <SlackConnectorFieldsInner
      key={configKey(currentConfig)}
      workspaceID={workspaceID}
      current={currentConfig}
      canEdit={canEdit}
      onToast={onToast}
    />
  )
}

type SlackConnectorFieldsInnerProps = Omit<SlackConnectorFieldsProps, "current"> & {
  current: SlackConnectorInput
}

function SlackConnectorFieldsInner({
  workspaceID,
  current,
  canEdit,
  onToast,
}: SlackConnectorFieldsInnerProps) {
  const { t } = useTranslation("admin")
  const mut = useUpdateWorkspaceSlackConnector(workspaceID)
  const createSecretMut = useCreateSecret(workspaceID)

  const [draft, setDraft] = useState<SlackConnectorInput>(current)
  const [secretInputs, setSecretInputs] = useState<SecretInputs>({ ...EMPTY_SECRET_INPUTS })
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const dirty = !configEqual(draft, current) || secretInputsDirty(secretInputs)
  const saving = mut.isPending || createSecretMut.isPending

  const missingRequired = missingRequiredFor(draft, secretInputs)
  const missingRequiredToEnable = missingRequiredFor({ ...draft, enabled: true }, secretInputs)
  const missingDraftIdentity = !draft.app_id.trim()

  const onSave = async (nextEnabled = draft.enabled) => {
    const nextDraft = { ...draft, enabled: nextEnabled }
    if (missingRequiredFor(nextDraft, secretInputs)) {
      setErrorMsg(t("connections.connector.slack.errors.incomplete"))
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
      onToast(t("connections.connector.slack.saved"))
    } catch (err) {
      if (err instanceof ApiError) {
        const code = err.envelope.code
        if (code === "slack_app_id_in_use") {
          setErrorMsg(t("connections.connector.slack.errors.appIdInUse"))
          return
        }
        if (code === "slack_connector_incomplete") {
          setErrorMsg(t("connections.connector.slack.errors.incomplete"))
          return
        }
      }
      setErrorMsg(
        err instanceof Error ? err.message : t("connections.connector.slack.errors.generic"),
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
      title={t("connections.connector.slack.title")}
      description={t("connections.connector.slack.description")}
      docHref={t("connections.connector.slack.docLink.href")}
      docLabel={t("connections.connector.slack.docLink.label")}
    >
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-line bg-surface-subtle px-3 py-2">
        <div className="min-w-0">
          <p className="text-sm font-medium text-fg">
            {draft.enabled
              ? t("connections.connector.status.enabled")
              : t("connections.connector.status.disabled")}
          </p>
          <p className="mt-0.5 text-xs text-fg-subtle">
            {t("connections.connector.slack.fields.enabled.hint")}
          </p>
        </div>
        <label className="inline-flex items-center gap-2 text-sm text-fg">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            disabled={!canEdit || saving}
            data-testid="slack-enabled-input"
          />
          {t("connections.connector.slack.fields.enabled.toggle")}
        </label>
      </div>

      <Field
        label={t("connections.connector.slack.fields.appId.label")}
        hint={t("connections.connector.slack.fields.appId.hint")}
        required
      >
        <input
          type="text"
          value={draft.app_id}
          placeholder="A0000000000000"
          onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
          disabled={!canEdit || saving}
          className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
          data-testid="slack-app-id-input"
        />
      </Field>

      <SecretInput
        label={t("connections.connector.slack.fields.botToken.label")}
        hint={t("connections.connector.slack.fields.botToken.hint")}
        savedHint={t("connections.connector.slack.fields.botToken.savedHint")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.botToken}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, botToken: v }))}
        required={!draft.bot_token_ref.trim()}
        hasSavedValue={Boolean(draft.bot_token_ref.trim())}
        disabled={!canEdit || saving}
        testId="slack-bot-token-input"
      />

      {draft.event_mode === "socket" ? (
        <SecretInput
          label={t("connections.connector.slack.fields.appToken.label")}
          hint={t("connections.connector.slack.fields.appToken.hint")}
          savedHint={t("connections.connector.slack.fields.appToken.savedHint")}
          savedBadge={t("connections.connector.savedBadge")}
          value={secretInputs.appToken}
          onChange={(v) => setSecretInputs((prev) => ({ ...prev, appToken: v }))}
          required={!draft.app_token_ref.trim()}
          hasSavedValue={Boolean(draft.app_token_ref.trim())}
          disabled={!canEdit || saving}
          testId="slack-app-token-input"
        />
      ) : (
        <SecretInput
          label={t("connections.connector.slack.fields.signingSecret.label")}
          hint={t("connections.connector.slack.fields.signingSecret.hint")}
          savedHint={t("connections.connector.slack.fields.signingSecret.savedHint")}
          savedBadge={t("connections.connector.savedBadge")}
          value={secretInputs.signingSecret}
          onChange={(v) => setSecretInputs((prev) => ({ ...prev, signingSecret: v }))}
          required={!draft.signing_secret_ref.trim()}
          hasSavedValue={Boolean(draft.signing_secret_ref.trim())}
          disabled={!canEdit || saving}
          testId="slack-signing-secret-input"
        />
      )}

      <Field
        label={t("connections.connector.slack.fields.eventMode.label")}
        hint={t("connections.connector.slack.fields.eventMode.hint")}
      >
        <div className="grid grid-cols-2 gap-2">
          {(["socket", "events"] as const).map((mode) => {
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
                {t(`connections.connector.slack.fields.eventMode.options.${mode}`)}
              </button>
            )
          })}
        </div>
      </Field>

      {!canEdit && (
        <p className="mt-3 text-sm text-fg-faint">{t("connections.connector.adminOnly")}</p>
      )}

      {errorMsg && (
        <p className="mt-3 text-sm text-danger" role="alert" data-testid="slack-error">
          {errorMsg}
        </p>
      )}

      <div className="mt-4 flex flex-wrap items-center justify-end gap-2">
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
          data-testid="slack-save-button"
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
            data-testid="slack-save-enable-button"
          >
            {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("connections.connector.actions.saveAndEnable")}
          </button>
        )}
      </div>
    </Card>
  )
}

function missingRequiredFor(draft: SlackConnectorInput, secretInputs: SecretInputs): boolean {
  return (
    draft.enabled &&
    (!draft.app_id.trim() ||
      (!draft.bot_token_ref.trim() && !secretInputs.botToken.trim()) ||
      (draft.event_mode === "socket"
        ? !draft.app_token_ref.trim() && !secretInputs.appToken.trim()
        : !draft.signing_secret_ref.trim() && !secretInputs.signingSecret.trim()))
  )
}

function configKey(config: SlackConnectorInput): string {
  return [
    config.enabled ? "1" : "0",
    config.app_id,
    config.bot_token_ref,
    config.app_token_ref,
    config.signing_secret_ref,
    config.event_mode,
  ].join("\u0000")
}

function applyChange(
  setDraft: (c: SlackConnectorInput) => void,
  sent: SlackConnectorInput,
  config: Record<string, unknown>,
) {
  const str = (k: string) => (typeof config[k] === "string" ? (config[k] as string) : "")
  const mode = str("event_mode")
  setDraft({
    enabled: sent.enabled,
    app_id: sent.app_id,
    bot_token_ref: str("bot_token_ref"),
    app_token_ref: str("app_token_ref"),
    signing_secret_ref: str("signing_secret_ref"),
    event_mode: mode === "events" ? "events" : "socket",
  })
}

function secretInputsDirty(inputs: SecretInputs): boolean {
  return Boolean(inputs.botToken.trim() || inputs.appToken.trim() || inputs.signingSecret.trim())
}

async function buildConfigWithSecretRefs(
  draft: SlackConnectorInput,
  inputs: SecretInputs,
  createSecret: (body: CreateSecretRequest) => Promise<string>,
): Promise<SlackConnectorInput> {
  const next = trimConfig(draft)

  for (const field of Object.keys(SLACK_SECRET_FIELDS) as SlackSecretField[]) {
    const plaintext = inputs[field].trim()
    if (!plaintext) continue
    const spec = SLACK_SECRET_FIELDS[field]
    next[spec.refKey] = await createSecret(createSlackSecretBody(spec, plaintext))
  }

  return next
}

function trimConfig(config: SlackConnectorInput): SlackConnectorInput {
  return {
    enabled: config.enabled,
    app_id: config.app_id.trim(),
    bot_token_ref: config.bot_token_ref.trim(),
    app_token_ref: config.app_token_ref.trim(),
    signing_secret_ref: config.signing_secret_ref.trim(),
    event_mode: config.event_mode === "events" ? "events" : "socket",
  }
}

function createSlackSecretBody(spec: SlackSecretFieldSpec, plaintext: string): CreateSecretRequest {
  return {
    name: spec.namePrefix + "-" + randomHex(6),
    kind: spec.kind,
    provider: "slack",
    auth_type: spec.authType,
    payload: { [spec.payloadKey]: plaintext },
  }
}

function configEqual(a: SlackConnectorInput, b: SlackConnectorInput): boolean {
  return (
    a.enabled === b.enabled &&
    a.app_id === b.app_id &&
    a.bot_token_ref === b.bot_token_ref &&
    a.app_token_ref === b.app_token_ref &&
    a.signing_secret_ref === b.signing_secret_ref &&
    a.event_mode === b.event_mode
  )
}
