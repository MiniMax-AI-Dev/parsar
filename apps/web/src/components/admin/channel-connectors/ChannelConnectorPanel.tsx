import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  useWorkspaceIMConnectors,
  readFeishuConnector,
  readSlackConnector,
  readDiscordConnector,
  readTeamsConnector,
  type ConnectorPlatform,
  type DiscordConnectorInput,
  type FeishuConnectorInput,
  type SlackConnectorInput,
  type TeamsConnectorInput,
} from "../../../lib/api-connectors"
import { Badge } from "../../ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/tabs"
import { FeishuConnectorFields } from "./feishuFields"
import { SlackConnectorFields } from "./slackFields"
import { DiscordConnectorFields } from "./discordFields"
import { TeamsConnectorFields } from "./teamsFields"

interface ChannelConnectorPanelProps {
  workspaceID: string | null
  canEdit: boolean
  onToast: (msg: string) => void
}

const PLATFORMS: ConnectorPlatform[] = ["feishu", "slack", "discord", "teams"]

type ConnectorStatus = "enabled" | "configured" | "incomplete" | "notConfigured"

const STATUS_VARIANT: Record<ConnectorStatus, "success" | "primary" | "warning" | "neutral"> = {
  enabled: "success",
  configured: "primary",
  incomplete: "warning",
  notConfigured: "neutral",
}

/**
 * One segmented tab per messaging platform; the active platform's form
 * below it, with its configuration state as a dot chip in the form head.
 */
export function ChannelConnectorPanel({
  workspaceID,
  canEdit,
  onToast,
}: ChannelConnectorPanelProps) {
  const { t } = useTranslation("admin")
  const { data } = useWorkspaceIMConnectors(workspaceID)
  const connectors = data?.connectors

  const feishuConfig = useMemo(() => readFeishuConnector(connectors), [connectors])
  const slackConfig = useMemo(() => readSlackConnector(connectors), [connectors])
  const discordConfig = useMemo(() => readDiscordConnector(connectors), [connectors])
  const teamsConfig = useMemo(() => readTeamsConnector(connectors), [connectors])

  // Until the user picks, the first configured platform is the active tab.
  const [picked, setPicked] = useState<ConnectorPlatform | null>(null)
  const configs = useMemo(
    () => ({ feishu: feishuConfig, slack: slackConfig, discord: discordConfig, teams: teamsConfig }),
    [feishuConfig, slackConfig, discordConfig, teamsConfig],
  )
  const platform: ConnectorPlatform =
    picked ?? PLATFORMS.find((p) => configs[p]?.app_id.trim()) ?? "feishu"

  const statusByPlatform = useMemo(() => {
    return Object.fromEntries(
      PLATFORMS.map((option) => {
        const config = configs[option]
        return [option, platformStatus(config, isPlatformComplete(option, config))]
      }),
    ) as Record<ConnectorPlatform, ConnectorStatus>
  }, [configs])

  const chip = (p: ConnectorPlatform) => (
    <Badge variant={STATUS_VARIANT[statusByPlatform[p]]} dot>
      {t(`connections.connector.platformList.status.${statusByPlatform[p]}`)}
    </Badge>
  )

  return (
    <Tabs
      value={platform}
      onValueChange={(v) => setPicked(v as ConnectorPlatform)}
      data-testid="channel-connector-panel"
    >
      <TabsList aria-label={t("connections.connector.platformList.title")}>
        {PLATFORMS.map((p) => (
          <TabsTrigger key={p} value={p} data-testid={`connector-platform-${p}`}>
            {t(`connections.connector.platformSelect.options.${p}`)}
          </TabsTrigger>
        ))}
      </TabsList>

      <TabsContent value="feishu">
        <FeishuConnectorFields
          workspaceID={workspaceID}
          current={feishuConfig}
          masterKeyConfigured={data?.master_key_configured}
          canEdit={canEdit}
          onToast={onToast}
          status={chip("feishu")}
        />
      </TabsContent>
      <TabsContent value="slack">
        <SlackConnectorFields
          workspaceID={workspaceID}
          current={slackConfig}
          canEdit={canEdit}
          onToast={onToast}
          status={chip("slack")}
        />
      </TabsContent>
      <TabsContent value="discord">
        <DiscordConnectorFields
          workspaceID={workspaceID}
          current={discordConfig}
          canEdit={canEdit}
          onToast={onToast}
          status={chip("discord")}
        />
      </TabsContent>
      <TabsContent value="teams">
        <TeamsConnectorFields
          workspaceID={workspaceID}
          current={teamsConfig}
          canEdit={canEdit}
          onToast={onToast}
          status={chip("teams")}
        />
      </TabsContent>
    </Tabs>
  )
}

function platformStatus(
  config:
    | FeishuConnectorInput
    | SlackConnectorInput
    | DiscordConnectorInput
    | TeamsConnectorInput
    | undefined,
  complete: boolean,
): ConnectorStatus {
  if (!config?.app_id.trim()) return "notConfigured"
  if (!complete) return "incomplete"
  return config.enabled ? "enabled" : "configured"
}

function isPlatformComplete(
  platform: ConnectorPlatform,
  config:
    | FeishuConnectorInput
    | SlackConnectorInput
    | DiscordConnectorInput
    | TeamsConnectorInput
    | undefined,
): boolean {
  if (!config?.app_id.trim()) return false
  switch (platform) {
    case "feishu": {
      const c = config as FeishuConnectorInput
      return Boolean(
        c.app_secret_ref.trim() &&
        (c.event_mode === "websocket" || c.verification_token_ref.trim()),
      )
    }
    case "slack": {
      const c = config as SlackConnectorInput
      return Boolean(
        c.bot_token_ref.trim() &&
        (c.event_mode === "socket" ? c.app_token_ref.trim() : c.signing_secret_ref.trim()),
      )
    }
    case "discord": {
      const c = config as DiscordConnectorInput
      return Boolean(c.bot_token_ref.trim() && c.public_key_ref.trim())
    }
    case "teams": {
      const c = config as TeamsConnectorInput
      return Boolean(c.app_password_ref.trim())
    }
  }
}
