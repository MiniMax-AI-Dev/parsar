import { useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useUpdateWorkspaceDiscordConnector,
  type DiscordConnectorInput,
} from "../../../lib/api-connectors"
import { useCreateSecret } from "../../../lib/api-secrets"
import type { CreateSecretRequest } from "../../../lib/api-types"
import { Button } from "../../ui/button"
import { Input } from "../../ui/input"
import { InlineError } from "../../runtime/InlineError"
import { EnabledField, Field, FormFooter, FormSection, SecretInput } from "./shared"
import { randomHex } from "../../../lib/random"

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
  /** State chip rendered in the section head. */
  status?: ReactNode
}

export function DiscordConnectorFields({
  workspaceID,
  current,
  canEdit,
  onToast,
  status,
}: DiscordConnectorFieldsProps) {
  const currentConfig = current ?? EMPTY_CONFIG
  return (
    <DiscordConnectorFieldsInner
      key={configKey(currentConfig)}
      workspaceID={workspaceID}
      current={currentConfig}
      canEdit={canEdit}
      onToast={onToast}
      status={status}
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
  status,
}: DiscordConnectorFieldsInnerProps) {
  const { t } = useTranslation("admin")
  const mut = useUpdateWorkspaceDiscordConnector(workspaceID)
  const createSecretMut = useCreateSecret(workspaceID)

  const [draft, setDraft] = useState<DiscordConnectorInput>(current)
  const [secretInputs, setSecretInputs] = useState<SecretInputs>({ ...EMPTY_SECRET_INPUTS })
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const dirty = !configEqual(draft, current) || secretInputsDirty(secretInputs)
  const saving = mut.isPending || createSecretMut.isPending
  const locked = !canEdit || saving

  const missingRequired = missingRequiredFor(draft, secretInputs)
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

  const onSave = async () => {
    if (missingRequiredFor(draft, secretInputs)) {
      setErrorMsg(t("connections.connector.discord.errors.incomplete"))
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

  return (
    <FormSection
      title={t("connections.connector.discord.title")}
      status={status}
      docHref={t("connections.connector.discord.docLink.href")}
      docLabel={t("connections.connector.discord.docLink.label")}
    >
      <EnabledField
        label={t("connections.connector.discord.fields.enabled.toggle")}
        checked={draft.enabled}
        onChange={(enabled) => setDraft({ ...draft, enabled })}
        disabled={locked}
        testId="discord-enabled-input"
      />

      <Field
        label={t("connections.connector.discord.fields.appId.label")}
        htmlFor="discord-app-id-input"
        required
      >
        <Input
          id="discord-app-id-input"
          type="text"
          value={draft.app_id}
          placeholder="1234567890"
          onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
          disabled={locked}
          className="font-mono"
          data-testid="discord-app-id-input"
        />
      </Field>

      <SecretInput
        label={t("connections.connector.discord.fields.botToken.label")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.botToken}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, botToken: v }))}
        required={!draft.bot_token_ref.trim()}
        hasSavedValue={Boolean(draft.bot_token_ref.trim())}
        disabled={locked}
        testId="discord-bot-token-input"
      />

      <SecretInput
        label={t("connections.connector.discord.fields.publicKey.label")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.publicKey}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, publicKey: v }))}
        required={!draft.public_key_ref.trim()}
        hasSavedValue={Boolean(draft.public_key_ref.trim())}
        disabled={locked}
        testId="discord-public-key-input"
      />

      <Field
        label={t("connections.connector.discord.fields.intents.label")}
      >
        <ul className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0">
          {DISCORD_INTENT_OPTIONS.map((intent) => (
            <li key={intent}>
              <label className="flex h-7 items-center gap-2 font-mono text-xs text-fg">
                <input
                  type="checkbox"
                  className="h-3.5 w-3.5 accent-accent"
                  checked={selectedIntents.includes(intent)}
                  onChange={() => toggleIntent(intent)}
                  disabled={locked}
                />
                {intent}
              </label>
            </li>
          ))}
        </ul>
      </Field>

      {!canEdit && <p className="text-xs text-fg-muted">{t("connections.connector.adminOnly")}</p>}
      {errorMsg && <InlineError data-testid="discord-error">{errorMsg}</InlineError>}

      <FormFooter>
        <Button
          onClick={() => void onSave()}
          disabled={locked || !dirty || missingDraftIdentity || Boolean(missingRequired)}
          data-testid="discord-save-button"
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
  ].join(" ")
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
