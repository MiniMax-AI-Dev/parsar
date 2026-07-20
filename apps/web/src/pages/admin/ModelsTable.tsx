import { useTranslation } from "react-i18next"
import {
  AlertCircle,
  CheckCircle2,
  Copy,
  Cpu,
  Database,
  Globe,
  KeyRound,
  Loader2,
  Pencil,
  Trash2,
  UserCircle,
  Zap,
} from "lucide-react"

import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../components/ui/table"
import { cn } from "../../lib/utils"
import { modelHealth } from "../../lib/model-health"
import { modelProtocols, protocolListLabel } from "../../lib/model-protocol"
import type { Model, ModelHealthStatus } from "../../lib/api-types"

function ModelHealthBadge({ model, isTesting }: { model: Model; isTesting: boolean }) {
  const { t } = useTranslation("admin")
  if (isTesting) {
    return (
      <Badge variant="primary" dot pulse>
        {t("models.health.checking")}
      </Badge>
    )
  }
  const health = modelHealth(model)
  const variant: Record<ModelHealthStatus, "success" | "destructive" | "warning" | "neutral"> = {
    healthy: "success",
    failed: "destructive",
    unsupported: "warning",
    untested: "neutral",
  }
  return (
    <Badge
      variant={variant[health.status]}
      dot={health.status !== "untested"}
      title={health.error ?? health.endpoint_type ?? undefined}
    >
      {t(`models.health.${health.status}`)}
    </Badge>
  )
}

function CredentialModeBadge({ mode }: { mode: Model["credential_mode"] }) {
  const { t } = useTranslation("admin")
  if (mode === "inline_secret") {
    return (
      <span className="inline-flex items-center gap-1 rounded-md bg-surface-muted px-1.5 py-0.5 text-xs font-medium text-fg-muted">
        <KeyRound className="h-3 w-3" />
        {t("models.credentialMode.shared")}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-md bg-info-subtle px-1.5 py-0.5 text-xs font-medium text-info-emphasis">
      <UserCircle className="h-3 w-3" />
      {t("models.credentialMode.personal")}
    </span>
  )
}

function ProviderCompatibilityCell({ type }: { type: string }) {
  const { t } = useTranslation("admin")
  let label: string
  switch (type) {
    case "openai":
      label = t("models.createProvider.providerTypeLabel.openai")
      break
    case "anthropic":
      label = t("models.createProvider.providerTypeLabel.anthropic")
      break
    case "anthropic-compatible":
      label = t("models.createProvider.providerTypeLabel.anthropicCompatible")
      break
    case "google":
      label = t("models.createProvider.providerTypeLabel.google")
      break
    case "openai-compatible":
      label = t("models.createProvider.providerTypeLabel.openaiCompatible")
      break
    default:
      label = type
  }

  const Icon =
    type === "openai" || type === "openai-compatible"
      ? Globe
      : type === "anthropic" || type === "anthropic-compatible"
        ? Cpu
        : Database

  return (
    <span
      className="inline-flex max-w-full items-center gap-1.5 text-sm text-fg-muted"
      title={label}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
      <span className="truncate">{label}</span>
    </span>
  )
}

function ModelProtocolCell({ model }: { model: Model }) {
  const { t } = useTranslation("admin")
  return (
    <Badge variant="neutral" title={t("models.createProvider.fields.protocol")}>
      {protocolListLabel(modelProtocols(model))}
    </Badge>
  )
}

export function ModelsTable({
  data,
  selectedIDs,
  testingModelID,
  onToggleModel,
  onToggleAllVisible,
  onRequestEdit,
  onRequestDelete,
  onRequestDuplicate,
  onTest,
  currentUserID,
  isAdmin,
}: {
  data: Model[]
  selectedIDs: Set<string>
  testingModelID: string | null
  onToggleModel: (modelID: string, selected: boolean) => void
  onToggleAllVisible: (selected: boolean) => void
  onRequestEdit: (m: Model) => void
  onRequestDelete: (m: Model) => void
  onRequestDuplicate: (m: Model) => void
  onTest: (m: Model) => void
  currentUserID: string | null
  isAdmin: boolean
}) {
  const { t } = useTranslation("admin")

  if (data.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-line bg-surface px-4 py-10 text-center text-sm text-fg-subtle">
        {t("models.empty.descriptionShort")}
      </div>
    )
  }

  const selectedVisibleCount = data.filter((m) => selectedIDs.has(m.id)).length
  const allVisibleSelected = data.length > 0 && selectedVisibleCount === data.length
  const someVisibleSelected = selectedVisibleCount > 0 && !allVisibleSelected

  return (
    <div className="overflow-hidden rounded-lg border border-line bg-surface">
      <Table className="table-fixed">
        <colgroup>
          <col className="w-[4%]" />
          <col className="w-[15%]" />
          <col className="w-[17%]" />
          <col className="w-[13%]" />
          <col className="w-[12%]" />
          <col className="w-[14%]" />
          <col className="w-[11%]" />
          <col className="w-[14%]" />
        </colgroup>
        <TableHeader>
          <TableRow>
            <TableHead className="pl-3">
              <input
                type="checkbox"
                className="h-3.5 w-3.5"
                checked={allVisibleSelected}
                ref={(node) => {
                  if (node) node.indeterminate = someVisibleSelected
                }}
                onChange={(event) => onToggleAllVisible(event.currentTarget.checked)}
                aria-label={t("models.bulkDelete.selectAll")}
              />
            </TableHead>
            <TableHead>{t("models.table.model")}</TableHead>
            <TableHead>{t("models.table.modelKey")}</TableHead>
            <TableHead>{t("models.table.compatibility")}</TableHead>
            <TableHead>{t("models.createProvider.fields.protocol")}</TableHead>
            <TableHead>{t("models.table.credentialMode")}</TableHead>
            <TableHead>{t("models.table.health")}</TableHead>
            <TableHead className="pr-3 text-right">{t("models.table.actions")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.map((m) => {
            const canEdit = isAdmin || (currentUserID && m.created_by === currentUserID)
            const isTesting = testingModelID === m.id
            const canTest = !isTesting && m.status === "active"
            const health = modelHealth(m)
            return (
              <TableRow
                key={m.id}
                className={cn(health.status !== "healthy" && "bg-surface-subtle/35 text-fg-muted")}
              >
                <TableCell className="pl-3">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5"
                    checked={selectedIDs.has(m.id)}
                    onChange={(event) => onToggleModel(m.id, event.currentTarget.checked)}
                    aria-label={t("models.bulkDelete.selectOne", { name: m.name })}
                  />
                </TableCell>
                <TableCell className="overflow-hidden">
                  <span
                    className={cn(
                      "block truncate text-sm font-medium",
                      health.status === "healthy" ? "text-fg" : "text-fg-muted",
                    )}
                    title={m.name}
                  >
                    {m.name}
                  </span>
                </TableCell>
                <TableCell className="overflow-hidden">
                  <span
                    className="block truncate font-mono text-sm text-fg-muted"
                    title={m.model_key}
                  >
                    {m.model_key}
                  </span>
                </TableCell>
                <TableCell className="overflow-hidden">
                  <ProviderCompatibilityCell type={m.provider_type} />
                </TableCell>
                <TableCell>
                  <ModelProtocolCell model={m} />
                </TableCell>
                <TableCell>
                  <CredentialModeBadge mode={m.credential_mode} />
                </TableCell>
                <TableCell>
                  <ModelHealthBadge model={m} isTesting={isTesting} />
                </TableCell>
                <TableCell className="pr-3 text-right">
                  <div className="inline-flex items-center gap-0.5">
                    <Button
                      variant="ghost"
                      size="icon"
                      shape="circle"
                      className="h-8 w-8"
                      onClick={() => onTest(m)}
                      disabled={!canTest}
                      title={t("models.actions.test")}
                      aria-label={t("models.actions.test")}
                    >
                      {isTesting ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        <Zap className="h-4 w-4" />
                      )}
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      shape="circle"
                      className="h-8 w-8"
                      onClick={() => onRequestEdit(m)}
                      disabled={!canEdit}
                      title={canEdit ? t("models.actions.edit") : t("models.actions.editForbidden")}
                      aria-label={t("models.actions.edit")}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      shape="circle"
                      className="h-8 w-8"
                      onClick={() => onRequestDuplicate(m)}
                      title={t("models.actions.copy")}
                      aria-label={t("models.actions.copy")}
                    >
                      <Copy className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-danger hover:bg-danger-subtle hover:text-danger-emphasis"
                      onClick={() => onRequestDelete(m)}
                      disabled={!canEdit}
                      title={canEdit ? t("models.actions.delete") : t("models.actions.deleteForbidden")}
                      aria-label={t("models.actions.delete")}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
