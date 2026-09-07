import { useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useUpdateWorkspaceSlackConnector,
  type SlackConnectorInput,
} from "../../../lib/api-connectors"
import { useCreateSecret } from "../../../lib/api-secrets"
import type { CreateSecretRequest } from "../../../lib/api-types"
import { Button } from "../../ui/button"
import { Input } from "../../ui/input"
import { Select } from "../../ui/select"
import { InlineError } from "../../runtime/InlineError"
import { EnabledField, Field, FormFooter, FormSection, SecretInput } from "./shared"
import { randomHex } from "../../../lib/random"

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
  /** State chip rendered in the section head. */
  status?: ReactNode
}

export function SlackConnectorFields({
  workspaceID,
  current,
  canEdit,
  onToast,
  status,
}: SlackConnectorFieldsProps) {
  const currentConfig = current ?? EMPTY_CONFIG
  return (
    <SlackConnectorFieldsInner
      key={configKey(currentConfig)}
      workspaceID={workspaceID}
      current={currentConfig}
      canEdit={canEdit}
      onToast={onToast}
      status={status}
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
  status,
}: SlackConnectorFieldsInnerProps) {
  const { t } = useTranslation("admin")
  const mut = useUpdateWorkspaceSlackConnector(workspaceID)
  const createSecretMut = useCreateSecret(workspaceID)

  const [draft, setDraft] = useState<SlackConnectorInput>(current)
  const [secretInputs, setSecretInputs] = useState<SecretInputs>({ ...EMPTY_SECRET_INPUTS })
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const dirty = !configEqual(draft, current) || secretInputsDirty(secretInputs)
  const saving = mut.isPending || createSecretMut.isPending
  const locked = !canEdit || saving

  const missingRequired = missingRequiredFor(draft, secretInputs)
  const missingDraftIdentity = !draft.app_id.trim()

  const onSave = async () => {
    if (missingRequiredFor(draft, secretInputs)) {
      setErrorMsg(t("connections.connector.slack.errors.incomplete"))
      return
    }
    setErrorMsg(null)
    try {
      const config = await buildConfigWithSecretRefs(draft, secretInputs, async (body) => {
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

  return (
    <FormSection
      title={t("connections.connector.slack.title")}
      status={status}
      docHref={t("connections.connector.slack.docLink.href")}
      docLabel={t("connections.connector.slack.docLink.label")}
    >
      <EnabledField
        label={t("connections.connector.slack.fields.enabled.toggle")}
        checked={draft.enabled}
        onChange={(enabled) => setDraft({ ...draft, enabled })}
        disabled={locked}
        testId="slack-enabled-input"
      />

      <Field
        label={t("connections.connector.slack.fields.appId.label")}
        htmlFor="slack-app-id-input"
        required
      >
        <Input
          id="slack-app-id-input"
          type="text"
          value={draft.app_id}
          placeholder="A0000000000000"
          onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
          disabled={locked}
          className="font-mono"
          data-testid="slack-app-id-input"
        />
      </Field>

      <SecretInput
        label={t("connections.connector.slack.fields.botToken.label")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.botToken}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, botToken: v }))}
        required={!draft.bot_token_ref.trim()}
        hasSavedValue={Boolean(draft.bot_token_ref.trim())}
        disabled={locked}
        placeholder="xoxb-…"
        testId="slack-bot-token-input"
      />

      <Field
        label={t("connections.connector.slack.fields.eventMode.label")}
        htmlFor="slack-event-mode"
      >
        <Select
          id="slack-event-mode"
          value={draft.event_mode}
          onChange={(e) => setDraft({ ...draft, event_mode: e.target.value === "events" ? "events" : "socket" })}
          disabled={locked}
        >
          {(["socket", "events"] as const).map((mode) => (
            <option key={mode} value={mode}>
              {t(`connections.connector.slack.fields.eventMode.options.${mode}`)}
            </option>
          ))}
        </Select>
      </Field>

      {draft.event_mode === "socket" ? (
        <SecretInput
          label={t("connections.connector.slack.fields.appToken.label")}
          savedBadge={t("connections.connector.savedBadge")}
          value={secretInputs.appToken}
          onChange={(v) => setSecretInputs((prev) => ({ ...prev, appToken: v }))}
          required={!draft.app_token_ref.trim()}
          hasSavedValue={Boolean(draft.app_token_ref.trim())}
          disabled={locked}
          placeholder="xapp-…"
          testId="slack-app-token-input"
        />
      ) : (
        <SecretInput
          label={t("connections.connector.slack.fields.signingSecret.label")}
          savedBadge={t("connections.connector.savedBadge")}
          value={secretInputs.signingSecret}
          onChange={(v) => setSecretInputs((prev) => ({ ...prev, signingSecret: v }))}
          required={!draft.signing_secret_ref.trim()}
          hasSavedValue={Boolean(draft.signing_secret_ref.trim())}
          disabled={locked}
          testId="slack-signing-secret-input"
        />
      )}

      {!canEdit && <p className="text-xs text-fg-muted">{t("connections.connector.adminOnly")}</p>}
      {errorMsg && <InlineError data-testid="slack-error">{errorMsg}</InlineError>}

      <FormFooter>
        <Button
          onClick={() => void onSave()}
          disabled={locked || !dirty || missingDraftIdentity || Boolean(missingRequired)}
          data-testid="slack-save-button"
        >
          {saving && <Loader2 className="animate-spin" />}
          {draft.enabled
            ? t("connections.connector.actions.save")
            : t("connections.connector.actions.saveDraft")}
        </Button>
      </FormFooter>
    </FormSection>
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
  ].join(" ")
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
