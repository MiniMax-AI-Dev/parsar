import { FeishuConnectorPanel } from "../../../components/admin/FeishuConnectorPanel"
import type { Agent } from "../../../lib/api-types"
import type { FeishuConnectorConfig } from "../../../lib/api-agents"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { useToast } from "../../../components/ui/toast"
import { DiscordConnectorFields } from "../../../components/admin/channel-connectors/discordFields"
import { FeishuConnectorFields } from "../../../components/admin/channel-connectors/feishuFields"
import { SlackConnectorFields } from "../../../components/admin/channel-connectors/slackFields"
import { TeamsConnectorFields } from "../../../components/admin/channel-connectors/teamsFields"
import { readDiscordConnector, readFeishuConnector, readSlackConnector, readTeamsConnector, useWorkspaceIMConnectors, type ConnectorPlatform } from "../../../lib/api-connectors"

export function AgentConnectionsField({ workspaceID, agent }: { workspaceID: string | null; agent?: Agent | null }) {
  const { t } = useTranslation("admin")
  const query = useWorkspaceIMConnectors(workspaceID)
  const [selected, setSelected] = useState<ConnectorPlatform | null>(null)
  const toast = useToast()
  const [dedicatedOpen, setDedicatedOpen] = useState(false)
  const connectors = agent?.config?.connectors as { feishu?: FeishuConnectorConfig } | undefined
  const configs = { feishu: readFeishuConnector(query.data?.connectors), slack: readSlackConnector(query.data?.connectors), discord: readDiscordConnector(query.data?.connectors), teams: readTeamsConnector(query.data?.connectors) }
  return <section className="space-y-2 border-t border-line pt-4">
    <h3 className="text-sm font-medium">{t("agentResources.connections")}</h3>
    <p className="text-xs text-fg-muted">{t("agentResources.workspaceScope")}</p>
    {query.isLoading && <p role="status">{t("agentResources.loading")}</p>}
    {Boolean(query.error) && <p role="alert">{t("agentResources.loadFailed")} <Button type="button" variant="ghost" onClick={() => void query.refetch()}>{t("agentResources.retry")}</Button></p>}
    {(["feishu", "slack", "discord", "teams"] as const).map(platform => <div key={platform} className="flex items-center justify-between gap-2"><span className="text-sm">{t(`connections.connector.platformSelect.options.${platform}`)} · {t(configs[platform]?.enabled ? "agentResources.enabled" : "agentResources.notEnabled")}</span><Button type="button" variant="ghost" disabled={query.isLoading || !!query.error} onClick={() => setSelected(platform)}>{t("agentResources.configure")}</Button></div>)}
    <div className="border-t border-line pt-2">
      {agent && workspaceID ? <><Button type="button" variant="outline" onClick={() => setDedicatedOpen(true)}>{t("agentResources.dedicatedFeishu")}</Button><Dialog open={dedicatedOpen} onOpenChange={setDedicatedOpen}><DialogContent className="max-h-[85dvh] overflow-y-auto"><DialogHeader><DialogTitle>{t("agentResources.dedicatedFeishu")}</DialogTitle></DialogHeader><FeishuConnectorPanel agentName={agent.name} agentID={agent.id} workspaceID={workspaceID} current={connectors?.feishu} canEdit onToast={toast.show} /></DialogContent></Dialog></> : <p className="text-xs text-fg-muted">{t("agentResources.dedicatedAfterCreate")}</p>}
    </div>
    <Dialog open={!!selected} onOpenChange={open => { if (!open) setSelected(null) }}><DialogContent className="max-h-[85dvh] overflow-y-auto"><DialogHeader><DialogTitle>{t("agentResources.connections")} · {selected}</DialogTitle></DialogHeader>
      {workspaceID && selected === "feishu" && <FeishuConnectorFields workspaceID={workspaceID} current={configs.feishu} masterKeyConfigured={query.data?.master_key_configured} canEdit onToast={toast.show} />}
      {workspaceID && selected === "slack" && <SlackConnectorFields workspaceID={workspaceID} current={configs.slack} canEdit onToast={toast.show} />}
      {workspaceID && selected === "discord" && <DiscordConnectorFields workspaceID={workspaceID} current={configs.discord} canEdit onToast={toast.show} />}
      {workspaceID && selected === "teams" && <TeamsConnectorFields workspaceID={workspaceID} current={configs.teams} canEdit onToast={toast.show} />}
    </DialogContent></Dialog>
  </section>
}
