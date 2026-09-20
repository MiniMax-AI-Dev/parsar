import { coreExecutionDefaults, coreHarnessLabel } from "../../../lib/core-api"
import { useTranslation } from "react-i18next"
import { PropertyList, Property } from "../../../components/ui/property-list"
import type { AgentDetail } from "../../../lib/api-types"
import { DetailSection } from "./DetailSection"

export const CONFIG_PROPERTY_LIST = ""

export function AgentConfigSummary({ agent }: { agent: AgentDetail; modelLabel: string }) {
  const { t } = useTranslation("admin")
  const defaults = coreExecutionDefaults(agent.config)
  return <DetailSection title={t("agents.core.configuration")}>
    <PropertyList>
      <Property label={t("agents.core.name")}>{agent.name}</Property>
      <Property label={t("agents.core.summary")}>{agent.description || "—"}</Property>
      <Property label={t("agents.core.model")}>{String(agent.config?.model ?? "—")}</Property>
      <Property label="Harness">{coreHarnessLabel(defaults.harness) || t("core.harnessNotSet")}</Property>
      <Property label={t("core.environment")}>{t(`core.environmentTypes.${defaults.environment.type}`)}</Property>
      {defaults.environment.type === "openai_hosted" && <Property label={t("core.template")}>{defaults.environment.environment_template_id || t("core.defaultEnvironment")}</Property>}
      <Property label={t("agents.form.instructions.label")} className="h-auto whitespace-pre-wrap [overflow-wrap:anywhere]">{String(agent.config?.system_prompt ?? "—")}</Property>
    </PropertyList>
    <p className="mt-3 text-sm text-fg-muted">{t("agents.core.newConversation")}</p>
  </DetailSection>
}
