import { useTranslation } from "react-i18next"
import type { AgentDetail } from "../../../lib/api-types"
import type { ShowToast } from "../../../components/ui/toast"
import { AgentConfigSummary } from "./AgentConfigSummary"
import { DetailSection } from "./DetailSection"

export function AgentConfigTab({ agent, modelLabel }: { agent: AgentDetail; workspaceID: string | null; workspaceRole?: string; modelLabel: string; onToast: ShowToast }) {
  const { t } = useTranslation("admin")
  return <div className="space-y-6">
    <AgentConfigSummary agent={agent} modelLabel={modelLabel} />
    <DetailSection title={t("core.environment")}><p className="text-sm text-fg-muted">{t("core.memberEnvironment")}</p></DetailSection>
    <DetailSection title={t("core.skillsTitle")}><p className="text-sm font-medium">{t("core.awaitingCore")}</p><p className="mt-2 text-sm text-fg-muted">{t("core.skillsPending")}</p></DetailSection>
  </div>
}
