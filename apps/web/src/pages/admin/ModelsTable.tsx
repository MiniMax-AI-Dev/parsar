/**
 * The models ledger: one 36px row per catalog model, grouped by provider.
 * Rows are multi-selectable (checkbox or row click) and feed the bulk-delete
 * footer on ModelsPage; row actions are test / edit / duplicate / delete.
 */
import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { Copy, Database, Pencil, Trash2, Zap } from "lucide-react"

import { ActionIconButton, RowActions } from "../../components/ui/action-button"
import { EmptyState } from "../../components/ui/empty-state"
import { Ledger, LedgerGroup, LedgerHeader, LedgerRow, col } from "../../components/ui/ledger"
import { StatusIcon, type StatusKind } from "../../components/ui/status-icon"
import { hostFromBaseURL } from "../../lib/model-base-url"
import { modelHealth } from "../../lib/model-health"
import { modelProtocols, protocolListLabel } from "../../lib/model-protocol"
import { FALLBACK_PROVIDER_TYPES } from "../../lib/model-provider-options"
import { useRelativeTime } from "../../lib/relative-time"
import type { Model } from "../../lib/api-types"

/** status icon · checkbox · model key · name · endpoint host · protocol · credential mode · last test · actions */
const LEDGER_COLUMNS = [col.icon(), col.check(), col.id(176, 1.2), col.title(160, 1), col.id(160), col.meta(104), col.meta(112), col.age(80), col.actions(4)]

interface CredentialStatus {
  labelKey: string
  icon: StatusKind
  detail?: string
}

/**
 * Whether the model can be used, as the 14px status icon: testing › disabled ›
 * pending credential › last test result. The word lives in the icon's title.
 */
function credentialStatus(model: Model, isTesting: boolean): CredentialStatus {
  if (isTesting) return { labelKey: "models.health.checking", icon: "running" }
  if (model.status === "disabled") return { labelKey: "models.status.disabled", icon: "cancelled" }
  if (model.credential_mode === "inline_secret" && !model.secret_id) {
    return { labelKey: "models.status.pending", icon: "queued" }
  }
  const health = modelHealth(model)
  const detail = health.error ?? health.endpoint_type
  switch (health.status) {
    case "healthy":
      return { labelKey: "models.health.healthy", icon: "completed", detail }
    case "failed":
      return { labelKey: "models.health.failed", icon: "failed", detail }
    case "unsupported":
      return { labelKey: "models.health.unsupported", icon: "interrupted", detail }
    default:
      return { labelKey: "models.health.untested", icon: "queued" }
  }
}

function useProviderLabel() {
  const { t } = useTranslation("admin")
  return (providerType: string): string => {
    const option = FALLBACK_PROVIDER_TYPES.find((p) => p.key === providerType)
    if (!option) return providerType
    if (option.label) return option.label
    return option.labelKey ? (t(option.labelKey as never) as unknown as string) : providerType
  }
}

export function ModelsTable({
  data,
  selectedIDs,
  testingModelIDs,
  onToggleModel,
  onRequestEdit,
  onRequestDelete,
  onRequestDuplicate,
  onTest,
  currentUserID,
  isAdmin,
}: {
  data: Model[]
  selectedIDs: Set<string>
  testingModelIDs: Set<string>
  onToggleModel: (modelID: string, selected: boolean) => void
  onRequestEdit: (m: Model) => void
  onRequestDelete: (m: Model) => void
  onRequestDuplicate: (m: Model) => void
  onTest: (m: Model) => void
  currentUserID: string | null
  isAdmin: boolean
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const providerLabel = useProviderLabel()

  const groups = useMemo(() => {
    const byProvider = new Map<string, Model[]>()
    for (const m of data) {
      const list = byProvider.get(m.provider_type) ?? []
      list.push(m)
      byProvider.set(m.provider_type, list)
    }
    return [...byProvider.entries()]
      .map(([providerType, models]) => ({
        providerType,
        label: providerLabel(providerType),
        models: [...models].sort((a, b) => a.name.localeCompare(b.name)),
      }))
      .sort((a, b) => a.label.localeCompare(b.label))
  }, [data, providerLabel])

  if (data.length === 0) {
    return <EmptyState icon={Database} title={t("models.empty.descriptionShort")} />
  }

  // Rows carry their own checkbox, so this is a plain list, not a listbox:
  // list/listitem instead of listbox/option.
  return (
    <Ledger columns={LEDGER_COLUMNS} role="list" aria-label={t("models.page.title")}>
      <LedgerHeader>
        <span />
        <span />
        <span>{t("models.table.modelKey")}</span>
        <span>{t("models.table.model")}</span>
        <span>{t("models.table.baseURL")}</span>
        <span>{t("models.createProvider.fields.protocol")}</span>
        <span>{t("models.table.credentialMode")}</span>
        <span className="text-right">{t("models.table.lastTested")}</span>
        <span />
      </LedgerHeader>
      {groups.map((g) => (
        <LedgerGroup key={g.providerType} label={g.label} count={g.models.length}>
          {g.models.map((m) => {
            const canEdit = isAdmin || (!!currentUserID && m.created_by === currentUserID)
            const isTesting = testingModelIDs.has(m.id)
            const selected = selectedIDs.has(m.id)
            const status = credentialStatus(m, isTesting)
            const statusLabel = t(status.labelKey as never) as unknown as string
            const health = modelHealth(m)
            const host = hostFromBaseURL(m.base_url)
            return (
              <LedgerRow
                key={m.id}
                role="listitem"
                aria-selected={undefined}
                tabIndex={-1}
                selected={selected}
                onClick={() => onToggleModel(m.id, !selected)}
              >
                <StatusIcon
                  status={status.icon}
                  title={status.detail ? `${statusLabel} · ${status.detail}` : statusLabel}
                />
                <input
                  type="checkbox"
                  className="h-3.5 w-3.5 accent-accent"
                  checked={selected}
                  onClick={(event) => event.stopPropagation()}
                  onChange={(event) => onToggleModel(m.id, event.currentTarget.checked)}
                  aria-label={t("models.bulkDelete.selectOne", { name: m.name })}
                />
                <span className="truncate font-mono text-xs text-fg" title={m.model_key}>
                  {m.model_key}
                </span>
                <span className="truncate font-medium" title={m.name}>
                  {m.name}
                </span>
                <span className="truncate font-mono text-xs text-fg" title={m.base_url}>
                  {host || "—"}
                </span>
                <span className="truncate text-xs text-fg-muted">{protocolListLabel(modelProtocols(m))}</span>
                <span className="truncate text-xs text-fg-muted">
                  {m.credential_mode === "inline_secret"
                    ? t("models.credentialMode.shared")
                    : t("models.credentialMode.personal")}
                </span>
                <span className="truncate text-right text-xs text-fg-muted">{fmtAgo(health.checked_at)}</span>
                <RowActions>
                  <ActionIconButton
                    icon={Zap}
                    label={t("models.actions.test")}
                    busy={isTesting}
                    disabled={m.status !== "active"}
                    onClick={() => onTest(m)}
                  />
                  <ActionIconButton
                    icon={Pencil}
                    label={canEdit ? t("models.actions.edit") : t("models.actions.editForbidden")}
                    disabled={!canEdit}
                    onClick={() => onRequestEdit(m)}
                  />
                  <ActionIconButton
                    icon={Copy}
                    label={t("models.actions.copy")}
                    onClick={() => onRequestDuplicate(m)}
                  />
                  <ActionIconButton
                    icon={Trash2}
                    tone="danger"
                    label={canEdit ? t("models.actions.delete") : t("models.actions.deleteForbidden")}
                    disabled={!canEdit}
                    onClick={() => onRequestDelete(m)}
                  />
                </RowActions>
              </LedgerRow>
            )
          })}
        </LedgerGroup>
      ))}
    </Ledger>
  )
}
