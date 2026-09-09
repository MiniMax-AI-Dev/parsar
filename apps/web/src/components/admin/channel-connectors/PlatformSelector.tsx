import { useTranslation } from "react-i18next"

import { Field } from "../../ui/label"
import { Select, SelectOption } from "../../ui/select"

export type ConnectorPlatform = "feishu" | "slack" | "discord" | "teams"

interface PlatformSelectorProps {
  value: ConnectorPlatform
  onChange: (next: ConnectorPlatform) => void
  disabled?: boolean
  testId?: string
}

/**
 * Select that picks which IM platform's connector config to render. Each
 * option maps 1:1 to a per-platform fields module and PATCH route.
 */
export function PlatformSelector({
  value,
  onChange,
  disabled = false,
  testId,
}: PlatformSelectorProps) {
  const { t } = useTranslation("admin")
  const options: ConnectorPlatform[] = ["feishu", "slack", "discord", "teams"]
  const id = testId ?? "channel-connector-platform"

  return (
    <Field label={t("connections.connector.platformSelect.label")} htmlFor={id} className="w-60">
      <Select
        id={id}
        value={value}
        onValueChange={(nextValue) => onChange(nextValue as ConnectorPlatform)}
        disabled={disabled}
        data-testid={testId ?? "channel-connector-platform-select"}
      >
        {options.map((opt) => (
          <SelectOption key={opt} value={opt}>
            {t(`connections.connector.platformSelect.options.${opt}`)}
          </SelectOption>
        ))}
      </Select>
    </Field>
  )
}
