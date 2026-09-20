import { useAgentCapabilitiesQuery } from "../../../lib/api-capabilities"
import { useTranslation } from "react-i18next"
import type { AgentDetail } from "../../../lib/api-types"
import type { ShowToast } from "../../../components/ui/toast"
import { AgentConfigSummary } from "./AgentConfigSummary"
import { DetailSection } from "./DetailSection"

export function AgentConfigTab({ agent, modelLabel, workspaceID }: { agent: AgentDetail; workspaceID: string | null; workspaceRole?: string; modelLabel: string; onToast: ShowToast }) {
  const { t } = useTranslation("admin")
  const resources = useAgentCapabilitiesQuery(workspaceID, agent.id)
  return <div className="space-y-6">
    <AgentConfigSummary agent={agent} modelLabel={modelLabel} />
    <DetailSection title={t("agentResources.title")}>
      {resources.isLoading && <p role="status">{t("agentResources.loading")}</p>}
      {Boolean(resources.error) && <p role="alert">{t("agentResources.loadFailed")}</p>}
      {resources.data?.installed.filter(item => !item.built_in && item.enabled).map(item => <div key={item.id} className="border-b border-line py-2 text-sm"><span className="font-medium">{item.capability?.name ?? item.capability_id}</span><span className="ml-2 text-fg-muted">{item.capability?.type} · {item.pinning_mode === "latest" ? t("agentResources.latest") : item.capability?.pinned_version ?? item.capability_version_id}</span>{item.capability?.type === "mcp" && <p className="mt-1 text-xs text-fg-muted">{t("agentResources.mcpPending")}</p>}</div>)}
    </DetailSection>
  </div>
}
