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

  const [platform, setPlatform] = useState<ConnectorPlatform>("feishu")

  const platformSummaries = useMemo(
    () =>
      PLATFORMS.map((option) => {
        const config = configForPlatform(option, {
          feishu: feishuConfig,
          slack: slackConfig,
          discord: discordConfig,
          teams: teamsConfig,
        })
        const appID = config?.app_id.trim() ?? ""
        const complete = isPlatformComplete(option, config)
        const status = platformStatus(config, complete)
        return { platform: option, appID, status }
      }),
    [discordConfig, feishuConfig, slackConfig, teamsConfig],
  )

  return (
    <div
      className="grid gap-4 xl:grid-cols-[minmax(0,360px)_minmax(0,1fr)]"
      data-testid="channel-connector-panel"
    >
      <section className="min-w-0">
        <div className="mb-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-fg-subtle">
            {t("connections.connector.platformList.title")}
          </h2>
          <p className="mt-1 text-sm text-fg-faint">
            {t("connections.connector.platformList.description")}
          </p>
        </div>
        <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-1">
          {platformSummaries.map((summary) => {
            const active = summary.platform === platform
            return (
              <button
                key={summary.platform}
                type="button"
                onClick={() => setPlatform(summary.platform)}
                className={`min-w-0 rounded-md border px-3 py-3 text-left transition ${
                  active
                    ? "border-line-strong bg-surface text-fg shadow-sm"
                    : "border-line bg-surface text-fg-muted hover:bg-surface-subtle"
                }`}
                aria-current={active ? "true" : undefined}
                data-testid={`connector-platform-${summary.platform}`}
              >
                <div className="flex min-w-0 items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">
                      {t(`connections.connector.platformSelect.options.${summary.platform}`)}
                    </p>
                    <p className="mt-1 truncate font-mono text-xs text-fg-faint">
                      {summary.appID || t("connections.connector.platformList.noAppId")}
                    </p>
                  </div>
                  <span
                    className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${statusClass(
                      summary.status,
                    )}`}
                  >
                    {t(`connections.connector.platformList.status.${summary.status}`)}
                  </span>
                </div>
              </button>
            )
          })}
        </div>
      </section>

      <div className="min-w-0">
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
        {platform === "teams" && (
          <TeamsConnectorFields
            workspaceID={workspaceID}
            current={teamsConfig}
            canEdit={canEdit}
            onToast={onToast}
          />
        )}
      </div>
    </div>
  )
}

function configForPlatform(
  platform: ConnectorPlatform,
  configs: {
    feishu: FeishuConnectorInput | undefined
    slack: SlackConnectorInput | undefined
    discord: DiscordConnectorInput | undefined
    teams: TeamsConnectorInput | undefined
  },
) {
  return configs[platform]
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

function statusClass(status: ConnectorStatus): string {
  switch (status) {
    case "enabled":
      return "bg-success-subtle text-success-emphasis"
    case "configured":
      return "bg-info-subtle text-info-emphasis"
    case "incomplete":
      return "bg-warning-subtle text-warning-emphasis"
    case "notConfigured":
      return "bg-surface-subtle text-fg-faint"
  }
}
