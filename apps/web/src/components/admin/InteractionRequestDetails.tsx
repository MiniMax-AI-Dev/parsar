import { useTranslation } from "react-i18next"
import type { AgentInteraction } from "../../lib/api-types"
import { Property, PropertyList } from "../ui/property-list"
import { VerbatimBlock } from "../ui/verbatim"

export function InteractionRequestDetails({ interaction }: { interaction: AgentInteraction }) {
  const { t } = useTranslation("admin")
  const payload = interaction.request.payload
  const entries = payload && typeof payload === "object" && !Array.isArray(payload) ? Object.entries(payload) : null
  const empty = payload == null || (typeof payload === "object" && Object.keys(payload).length === 0)
  const valueText = (value: unknown) => typeof value === "string" ? value : JSON.stringify(value, null, 2)

  return (
    <section className="space-y-3">
      {typeof interaction.request.action === "string" && interaction.request.action && (
        <PropertyList>
          <Property label={t("approvals.detail.operation")} mono className="h-auto min-h-7 items-start whitespace-normal py-1">
            <span className="break-all">{interaction.request.action}</span>
          </Property>
        </PropertyList>
      )}
      <h3 className="text-base font-semibold">{t("approvals.detail.payload")}</h3>
      {empty ? (
        <p className="text-sm text-fg-muted">{t("approvals.detail.noParameters")}</p>
      ) : entries ? (
        <PropertyList>
          {entries.map(([key, value]) => (
            <Property key={key} label={key} className="h-auto min-h-7 items-start whitespace-normal py-1">
              <VerbatimBlock className="w-full min-w-0 max-h-52 break-all">{valueText(value)}</VerbatimBlock>
            </Property>
          ))}
        </PropertyList>
      ) : <VerbatimBlock className="w-full min-w-0 max-h-52 break-all">{valueText(payload)}</VerbatimBlock>}
      {interaction.kind === "permission" && interaction.status === "pending" && <p className="text-xs text-fg-muted">{t("approvals.detail.onceScope")}</p>}
    </section>
  )
}
