import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2, RefreshCw } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useUpdateWorkspaceTeamsConnector,
  type TeamsConnectorInput,
} from "../../../lib/api-connectors"
import { useCreateSecret } from "../../../lib/api-secrets"
import type { CreateSecretRequest } from "../../../lib/api-types"
import { Card, Field, SecretInput, randomHex } from "./shared"

const EMPTY_CONFIG: TeamsConnectorInput = {
  enabled: false,
  app_id: "",
  app_password_ref: "",
  tenant_id: "",
}

type SecretInputs = {
  appPassword: string
}

type TeamsSecretField = keyof SecretInputs
type TeamsSecretRefKey = "app_password_ref"

type TeamsSecretFieldSpec = {
  refKey: TeamsSecretRefKey
  kind: string
  authType: string
  payloadKey: string
  namePrefix: string
}

const EMPTY_SECRET_INPUTS: SecretInputs = {
  appPassword: "",
}

const TEAMS_SECRET_FIELDS: Record<TeamsSecretField, TeamsSecretFieldSpec> = {
  appPassword: {
    refKey: "app_password_ref",
    kind: "teams_app_password",
    authType: "app_password",
    payloadKey: "app_password",
    namePrefix: "teams-app-password",
  },
}

export interface TeamsConnectorFieldsProps {
  workspaceID: string | null
  current: TeamsConnectorInput | undefined
  canEdit: boolean
  onToast: (msg: string) => void
}

export function TeamsConnectorFields({
  workspaceID,
  current,
  canEdit,
  onToast,
}: TeamsConnectorFieldsProps) {
  const currentConfig = current ?? EMPTY_CONFIG
  return (
    <TeamsConnectorFieldsInner
      key={configKey(currentConfig)}
      workspaceID={workspaceID}
      current={currentConfig}
      canEdit={canEdit}
      onToast={onToast}
    />
  )
}

type TeamsConnectorFieldsInnerProps = Omit<TeamsConnectorFieldsProps, "current"> & {
  current: TeamsConnectorInput
}

function TeamsConnectorFieldsInner({
  workspaceID,
  current,
  canEdit,
  onToast,
}: TeamsConnectorFieldsInnerProps) {
  const { t } = useTranslation("admin")
  const mut = useUpdateWorkspaceTeamsConnector(workspaceID)
  const createSecretMut = useCreateSecret(workspaceID)

  const [draft, setDraft] = useState<TeamsConnectorInput>(current)
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
      setErrorMsg(t("connections.connector.teams.errors.incomplete"))
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
      onToast(t("connections.connector.teams.saved"))
    } catch (err) {
      if (err instanceof ApiError) {
        const code = err.envelope.code
        if (code === "teams_app_id_in_use") {
          setErrorMsg(t("connections.connector.teams.errors.appIdInUse"))
          return
        }
        if (code === "teams_connector_incomplete") {
          setErrorMsg(t("connections.connector.teams.errors.incomplete"))
          return
        }
      }
      setErrorMsg(
        err instanceof Error ? err.message : t("connections.connector.teams.errors.generic"),
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
      title={t("connections.connector.teams.title")}
      description={t("connections.connector.teams.description")}
      docHref={t("connections.connector.teams.docLink.href")}
      docLabel={t("connections.connector.teams.docLink.label")}
    >
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-line bg-surface-subtle px-3 py-2">
        <div className="min-w-0">
          <p className="text-sm font-medium text-fg">
            {draft.enabled
              ? t("connections.connector.status.enabled")
              : t("connections.connector.status.disabled")}
          </p>
          <p className="mt-0.5 text-xs text-fg-subtle">
            {t("connections.connector.teams.fields.enabled.hint")}
          </p>
        </div>
        <label className="inline-flex items-center gap-2 text-sm text-fg">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            disabled={!canEdit || saving}
            data-testid="teams-enabled-input"
          />
          {t("connections.connector.teams.fields.enabled.toggle")}
        </label>
      </div>

      <Field
        label={t("connections.connector.teams.fields.appId.label")}
        hint={t("connections.connector.teams.fields.appId.hint")}
        required
      >
        <input
          type="text"
          value={draft.app_id}
          placeholder="00000000-0000-0000-0000-000000000000"
          onChange={(e) => setDraft({ ...draft, app_id: e.target.value })}
          disabled={!canEdit || saving}
          className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
          data-testid="teams-app-id-input"
        />
      </Field>

      <SecretInput
        label={t("connections.connector.teams.fields.appPassword.label")}
        hint={t("connections.connector.teams.fields.appPassword.hint")}
        savedHint={t("connections.connector.teams.fields.appPassword.savedHint")}
        savedBadge={t("connections.connector.savedBadge")}
        value={secretInputs.appPassword}
        onChange={(v) => setSecretInputs((prev) => ({ ...prev, appPassword: v }))}
        required={!draft.app_password_ref.trim()}
        hasSavedValue={Boolean(draft.app_password_ref.trim())}
        disabled={!canEdit || saving}
        testId="teams-app-password-input"
      />

      <Field
        label={t("connections.connector.teams.fields.tenantId.label")}
        hint={t("connections.connector.teams.fields.tenantId.hint")}
      >
        <input
          type="text"
          value={draft.tenant_id}
          placeholder="common"
          onChange={(e) => setDraft({ ...draft, tenant_id: e.target.value })}
          disabled={!canEdit || saving}
          className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
          data-testid="teams-tenant-id-input"
        />
      </Field>

      {!canEdit && (
        <p className="mt-3 text-sm text-fg-faint">{t("connections.connector.adminOnly")}</p>
      )}

      {errorMsg && (
        <p className="mt-3 text-sm text-danger" role="alert" data-testid="teams-error">
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
          data-testid="teams-save-button"
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
            data-testid="teams-save-enable-button"
          >
            {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("connections.connector.actions.saveAndEnable")}
          </button>
        )}
      </div>
    </Card>
  )
}

function missingRequiredFor(draft: TeamsConnectorInput, secretInputs: SecretInputs): boolean {
  return (
    draft.enabled &&
    (!draft.app_id.trim() || (!draft.app_password_ref.trim() && !secretInputs.appPassword.trim()))
  )
}

function configKey(config: TeamsConnectorInput): string {
  return [
    config.enabled ? "1" : "0",
    config.app_id,
    config.app_password_ref,
    config.tenant_id,
  ].join("\u0000")
}

function applyChange(
  setDraft: (c: TeamsConnectorInput) => void,
  sent: TeamsConnectorInput,
  config: Record<string, unknown>,
) {
  const str = (k: string) => (typeof config[k] === "string" ? (config[k] as string) : "")
  setDraft({
    enabled: sent.enabled,
    app_id: sent.app_id,
    app_password_ref: str("app_password_ref"),
    tenant_id: str("tenant_id"),
  })
}

function secretInputsDirty(inputs: SecretInputs): boolean {
  return Boolean(inputs.appPassword.trim())
}

async function buildConfigWithSecretRefs(
  draft: TeamsConnectorInput,
  inputs: SecretInputs,
  createSecret: (body: CreateSecretRequest) => Promise<string>,
): Promise<TeamsConnectorInput> {
  const next = trimConfig(draft)

  for (const field of Object.keys(TEAMS_SECRET_FIELDS) as TeamsSecretField[]) {
    const plaintext = inputs[field].trim()
    if (!plaintext) continue
    const spec = TEAMS_SECRET_FIELDS[field]
    next[spec.refKey] = await createSecret(createTeamsSecretBody(spec, plaintext))
  }

  return next
}

function trimConfig(config: TeamsConnectorInput): TeamsConnectorInput {
  return {
    enabled: config.enabled,
    app_id: config.app_id.trim(),
    app_password_ref: config.app_password_ref.trim(),
    tenant_id: config.tenant_id.trim(),
  }
}

function createTeamsSecretBody(spec: TeamsSecretFieldSpec, plaintext: string): CreateSecretRequest {
  return {
    name: spec.namePrefix + "-" + randomHex(6),
    kind: spec.kind,
    provider: "teams",
    auth_type: spec.authType,
    payload: { [spec.payloadKey]: plaintext },
  }
}

function configEqual(a: TeamsConnectorInput, b: TeamsConnectorInput): boolean {
  return (
    a.enabled === b.enabled &&
    a.app_id === b.app_id &&
    a.app_password_ref === b.app_password_ref &&
    a.tenant_id === b.tenant_id
  )
}
