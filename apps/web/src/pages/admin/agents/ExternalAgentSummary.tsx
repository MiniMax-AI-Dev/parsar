import { useTranslation } from "react-i18next"
import { Property, PropertyList } from "../../../components/ui/property-list"
import { httpAgentConfig } from "../../../lib/http-agent"
import type { AgentDetail } from "../../../lib/api-types"
import { DetailSection } from "./DetailSection"

export function ExternalAgentSummary({ agent }: { agent: AgentDetail }) {
  const { t } = useTranslation("admin")
  const config = httpAgentConfig(agent.config)
  return <DetailSection title={t("agents.execution.external.title")}>
    <p className="mb-3 text-sm text-fg-muted">{t("agents.http.ownership")}</p>
    <PropertyList>
      <Property label={t("agents.http.endpoint")}>{config.endpoint || "—"}</Property>
      <Property label={t("agents.http.authentication")}>{t(config.secretID ? "agents.http.savedCredential" : "agents.http.noAuth")}</Property>
      <Property label={t("agents.form.instructions.label")}>{String(agent.config?.system_prompt ?? "") || "—"}</Property>
    </PropertyList>
  </DetailSection>
}
