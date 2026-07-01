import { useTranslation } from "react-i18next"

export type ConnectorPlatform = "feishu" | "slack" | "discord" | "teams"

interface PlatformSelectorProps {
  value: ConnectorPlatform
  onChange: (next: ConnectorPlatform) => void
  disabled?: boolean
  testId?: string
}

/**
 * Dropdown that lets the admin pick which IM platform's connector config to
 * render in the ChannelConnectorPanel. Each option maps 1:1 to a per-platform
 * fields module (feishuFields / slackFields / discordFields) and a per-platform
 * PATCH route on the backend.
 */
export function PlatformSelector({
  value,
  onChange,
  disabled = false,
  testId,
}: PlatformSelectorProps) {
  const { t } = useTranslation("admin")
  const options: ConnectorPlatform[] = ["feishu", "slack", "discord", "teams"]

  return (
    <div className="mb-3 flex items-center justify-between gap-3 rounded-md border border-line bg-surface px-3 py-2">
      <label
        htmlFor={testId ?? "channel-connector-platform"}
        className="text-xs uppercase tracking-wider text-fg-subtle"
      >
        {t("connections.connector.platformSelect.label")}
      </label>
      <select
        id={testId ?? "channel-connector-platform"}
        value={value}
        onChange={(e) => onChange(e.target.value as ConnectorPlatform)}
        disabled={disabled}
        className="h-8 rounded-md border border-line bg-surface px-2 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:cursor-not-allowed disabled:bg-surface-subtle"
        data-testid={testId ?? "channel-connector-platform-select"}
      >
        {options.map((opt) => (
          <option key={opt} value={opt}>
            {t(`connections.connector.platformSelect.options.${opt}`)}
          </option>
        ))}
      </select>
    </div>
  )
}