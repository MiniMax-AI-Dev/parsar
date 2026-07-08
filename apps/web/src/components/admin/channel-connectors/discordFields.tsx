import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2, RefreshCw } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useUpdateWorkspaceDiscordConnector,
  type DiscordConnectorInput,
} from "../../../lib/api-connectors"
import { useCreateSecret } from "../../../lib/api-secrets"
import type { CreateSecretRequest } from "../../../lib/api-types"
import { Card, Field, SecretInput, randomHex } from "./shared"

const EMPTY_CONFIG: DiscordConnectorInput = {
  enabled: false,
  app_id: "",
  bot_token_ref: "",
  public_key_ref: "",
  intents: "",
}

const DISCORD_INTENT_OPTIONS = [
  "GUILDS",
  "GUILD_MESSAGES",
  "DIRECT_MESSAGES",
  "MESSAGE_CONTENT",
  "GUILD_MESSAGE_REACTIONS",
] as const

type SecretInputs = {
  botToken: string
  publicKey: string
}

type DiscordSecretField = keyof SecretInputs
type DiscordSecretRefKey = "bot_token_ref" | "public_key_ref"

type DiscordSecretFieldSpec = {
  refKey: DiscordSecretRefKey
  kind: string
  authType: string
  payloadKey: string
  namePrefix: string
}

const EMPTY_SECRET_INPUTS: SecretInputs = {
  botToken: "",
  publicKey: "",
}

const DISCORD_SECRET_FIELDS: Record<DiscordSecretField, DiscordSecretFieldSpec> = {
  botToken: {
    refKey: "bot_token_ref",
    kind: "discord_bot_token",
    authType: "bot_token",
    payloadKey: "bot_token",
    namePrefix: "discord-bot-token",
  },
  publicKey: {
    refKey: "public_key_ref",
    kind: "discord_public_key",
    authType: "public_key",
    payloadKey: "public_key",
    namePrefix: "discord-public-key",
  },
}

export interface DiscordConnectorFieldsProps {
  workspaceID: string | null
  current: DiscordConnectorInput | undefined
  canEdit: boolean
  onToast: (msg: string) => void
}

export function DiscordConnectorFields({
  workspaceID,
  current,
  canEdit,
  onToast,
}: DiscordConnectorFieldsProps) {
  const currentConfig = current ?? EMPTY_CONFIG
  return (
    <DiscordConnectorFieldsInner
      key={configKey(currentConfig)}
      workspaceID={workspaceID}
      current={currentConfig}
      canEdit={canEdit}
      onToast={onToast}
    />
  )
}

type DiscordConnectorFieldsInnerProps = Omit<DiscordConnectorFieldsProps, "current"> & {
  current: DiscordConnectorInput
}

function DiscordConnectorFieldsInner({
  workspaceID,
  current,
  canEdit,
  onToast,
}: DiscordConnectorFieldsInnerProps) {
  const { t } = useTranslation("admin")
  const mut = useUpdateWorkspaceDiscordConnector(workspaceID)
  const createSecretMut = useCreateSecret(workspaceID)

  const [draft, setDraft] = useState<DiscordConnectorInput>(current)
  const [secretInputs, setSecretInputs] = useState<SecretInputs>({ ...EMPTY_SECRET_INPUTS })
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const dirty = !configEqual(draft, current) || secretInputsDirty(secretInputs)
  const saving = mut.isPending || createSecretMut.isPending

  const missingRequired = missingRequiredFor(draft, secretInputs)
  const missingRequiredToEnable = missingRequiredFor({ ...draft, enabled: true }, secretInputs)
  const missingDraftIdentity = !draft.app_id.trim()

  const selectedIntents = parseIntents(draft.intents)

  const toggleIntent = (intent: string) => {
    setDraft((prev) => {
      const set = new Set(parseIntents(prev.intents))
      if (set.has(intent)) {
        set.delete(intent)
      } else {
        set.add(intent)
      }
      return { ...prev, intents: Array.from(set).join(",") }
    })
  }

  const onSave = async (nextEnabled = draft.enabled) => {
    const nextDraft = { ...draft, enabled: nextEnabled }
    if (missingRequiredFor(nextDraft, secretInputs)) {
      setErrorMsg(t("connections.connector.discord.errors.incomplete"))
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
      onToast(t("connections.connector.discord.saved"))
    } catch (err) {
      if (err instanceof ApiError) {
        const code = err.envelope.code
        if (code === "discord_app_id_in_use") {
          setErrorMsg(t("connections.connector.discord.errors.appIdInUse"))
          return
        }
        if (code === "discord_connector_incomplete") {
          setErrorMsg(t("connections.connector.discord.errors.incomplete"))
          return
        }
      }
      setErrorMsg(
        err instanceof Error ? err.message : t("connections.connector.discord.errors.generic"),
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
      title={t("connections.connector.discord.title")}
      description={t("connections.connector.discord.description")}
      docHref={t("connections.connector.discord.docLink.href")}
      docLabel={t("connections.connector.discord.docLink.label")}
    >
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-line bg-surface-subtle px-3 py-2">
        <div className="min-w-0">
          <p className="text-sm font-medium text-fg">
            {draft.enabled
              ? t("connections.connector.status.enabled")
              : t("connections.connector.status.disabled")}
          </p>
          <p className="mt-0.5 text-xs text-fg-subtle">
            {t("connections.connector.discord.fields.enabled.hint")}
          </p>
        </div>
        <label className="inline-flex items-center gap-2 text-sm text-fg">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            disabled={!canEdit || saving}
            data-testid="discord-enabled-input"
          />
          {t("connections.connector.discord.fields.enabled.toggle")}
        </label>
      </div>

      <Field
        label={t("connections.connector.discord.fields.appId.label")}
        hint={t("connections.connector.discord.fields.appId.hint")}
        required
      >
        <input
          type="text"
          value={draft.app_id}
          placeholder="1234567890"
          onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
          disabled={!canEdit || saving}
          className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
          data-testid="discord-app-id-input"
        />
      </Field>

      <SecretInput
        label={t("connections.connector.discord.fields.botToken.label")}
        hint={t("connections.connector.discord.fields.botToken.hint")}
        savedHint={t("connections.connector.discord.fields.botToken.savedHint")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.botToken}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, botToken: v }))}
        required={!draft.bot_token_ref.trim()}
        hasSavedValue={Boolean(draft.bot_token_ref.trim())}
        disabled={!canEdit || saving}
        testId="discord-bot-token-input"
      />

      <SecretInput
        label={t("connections.connector.discord.fields.publicKey.label")}
        hint={t("connections.connector.discord.fields.publicKey.hint")}
        savedHint={t("connections.connector.discord.fields.publicKey.savedHint")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.publicKey}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, publicKey: v }))}
        required={!draft.public_key_ref.trim()}
        hasSavedValue={Boolean(draft.public_key_ref.trim())}
        disabled={!canEdit || saving}
        testId="discord-public-key-input"
      />

      <Field
        label={t("connections.connector.discord.fields.intents.label")}
        hint={t("connections.connector.discord.fields.intents.hint")}
      >
        <div className="flex flex-wrap gap-2">
          {DISCORD_INTENT_OPTIONS.map((intent) => {
            const active = selectedIntents.includes(intent)
            return (
              <button
                key={intent}
                type="button"
                onClick={() => toggleIntent(intent)}
                disabled={!canEdit || saving}
                className={`min-h-8 rounded-md border px-2.5 py-1 font-mono text-xs transition ${
                  active
                    ? "border-line-strong bg-surface-emphasis text-white"
                    : "border-line bg-surface text-fg-muted hover:bg-surface-subtle"
                } disabled:opacity-60`}
                aria-pressed={active}
              >
                {intent}
              </button>
            )
          })}
        </div>
      </Field>

      {!canEdit && (
        <p className="mt-3 text-sm text-fg-faint">{t("connections.connector.adminOnly")}</p>
      )}

      {errorMsg && (
        <p className="mt-3 text-sm text-danger" role="alert" data-testid="discord-error">
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
          data-testid="discord-save-button"
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
            data-testid="discord-save-enable-button"
          >
            {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("connections.connector.actions.saveAndEnable")}
          </button>
        )}
      </div>
    </Card>
  )
}

function missingRequiredFor(draft: DiscordConnectorInput, secretInputs: SecretInputs): boolean {
  return (
    draft.enabled &&
    (!draft.app_id.trim() ||
      (!draft.bot_token_ref.trim() && !secretInputs.botToken.trim()) ||
      (!draft.public_key_ref.trim() && !secretInputs.publicKey.trim()))
  )
}

function configKey(config: DiscordConnectorInput): string {
  return [
    config.enabled ? "1" : "0",
    config.app_id,
    config.bot_token_ref,
    config.public_key_ref,
    config.intents,
  ].join("\u0000")
}

function applyChange(
  setDraft: (c: DiscordConnectorInput) => void,
  sent: DiscordConnectorInput,
  config: Record<string, unknown>,
) {
  const str = (k: string) => (typeof config[k] === "string" ? (config[k] as string) : "")
  setDraft({
    enabled: sent.enabled,
    app_id: sent.app_id,
    bot_token_ref: str("bot_token_ref"),
    public_key_ref: str("public_key_ref"),
    intents: str("intents"),
  })
}

function parseIntents(intents: string): string[] {
  return intents
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
}

function secretInputsDirty(inputs: SecretInputs): boolean {
  return Boolean(inputs.botToken.trim() || inputs.publicKey.trim())
}

async function buildConfigWithSecretRefs(
  draft: DiscordConnectorInput,
  inputs: SecretInputs,
  createSecret: (body: CreateSecretRequest) => Promise<string>,
): Promise<DiscordConnectorInput> {
  const next = trimConfig(draft)

  for (const field of Object.keys(DISCORD_SECRET_FIELDS) as DiscordSecretField[]) {
    const plaintext = inputs[field].trim()
    if (!plaintext) continue
    const spec = DISCORD_SECRET_FIELDS[field]
    next[spec.refKey] = await createSecret(createDiscordSecretBody(spec, plaintext))
  }

  return next
}

function trimConfig(config: DiscordConnectorInput): DiscordConnectorInput {
  return {
    enabled: config.enabled,
    app_id: config.app_id.trim(),
    bot_token_ref: config.bot_token_ref.trim(),
    public_key_ref: config.public_key_ref.trim(),
    intents: parseIntents(config.intents).sort().join(","),
  }
}

function createDiscordSecretBody(
  spec: DiscordSecretFieldSpec,
  plaintext: string,
): CreateSecretRequest {
  return {
    name: spec.namePrefix + "-" + randomHex(6),
    kind: spec.kind,
    provider: "discord",
    auth_type: spec.authType,
    payload: { [spec.payloadKey]: plaintext },
  }
}

function configEqual(a: DiscordConnectorInput, b: DiscordConnectorInput): boolean {
  return (
    a.enabled === b.enabled &&
    a.app_id === b.app_id &&
    a.bot_token_ref === b.bot_token_ref &&
    a.public_key_ref === b.public_key_ref &&
    parseIntents(a.intents).sort().join(",") === parseIntents(b.intents).sort().join(",")
  )
}
