import { useMemo, useState } from "react"

import {
  useWorkspaceIMConnectors,
  readFeishuConnector,
  readSlackConnector,
  readDiscordConnector,
} from "../../../lib/api-connectors"
import { FeishuConnectorFields } from "./feishuFields"
import { SlackConnectorFields } from "./slackFields"
import { DiscordConnectorFields } from "./discordFields"
import { PlatformSelector, type ConnectorPlatform } from "./PlatformSelector"

interface ChannelConnectorPanelProps {
  workspaceID: string | null
  canEdit: boolean
  onToast: (msg: string) => void
}

/**
 * Top-level workspace-dimension connector configuration surface.
 *
 * The PlatformSelector dropdown lets the admin pick Feishu / Slack / Discord,
 * and the panel swaps in the matching per-platform fields sub-component. The
 * persisted config for every platform is read once from
 * `GET /workspaces/{id}/connectors` and split per-platform; switching platforms
 * preserves the persisted config and resets any in-flight draft, since each
 * platform has its own validation gates and own secret fields.
 */
export function ChannelConnectorPanel({
  workspaceID,
  canEdit,
  onToast,
}: ChannelConnectorPanelProps) {
  const { data } = useWorkspaceIMConnectors(workspaceID)
  const connectors = data?.connectors

  const feishuConfig = useMemo(() => readFeishuConnector(connectors), [connectors])
  const slackConfig = useMemo(() => readSlackConnector(connectors), [connectors])
  const discordConfig = useMemo(() => readDiscordConnector(connectors), [connectors])

  const [platform, setPlatform] = useState<ConnectorPlatform>("feishu")

  return (
    <div data-testid="channel-connector-panel">
      <PlatformSelector value={platform} onChange={setPlatform} />
      {platform === "feishu" && (
        <FeishuConnectorFields
          workspaceID={workspaceID}
          current={feishuConfig}
          canEdit={canEdit}
          onToast={onToast}
        />
      )}
      {platform === "slack" && (
        <SlackConnectorFields
          workspaceID={workspaceID}
          current={slackConfig}
          canEdit={canEdit}
          onToast={onToast}
        />
      )}
      {platform === "discord" && (
        <DiscordConnectorFields
          workspaceID={workspaceID}
          current={discordConfig}
          canEdit={canEdit}
          onToast={onToast}
        />
      )}
    </div>
  )
}
