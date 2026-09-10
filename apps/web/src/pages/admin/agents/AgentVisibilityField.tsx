import { useId } from "react"
import { useTranslation } from "react-i18next"
import { Label } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"
import type { AgentVisibility } from "../../../lib/api-agents"

export function AgentVisibilityField({ value, onChange, disabled }: {
  value: AgentVisibility
  onChange: (value: AgentVisibility) => void
  disabled: boolean
}) {
  const { t } = useTranslation("admin")
  const id = useId()
  return (
    <div className="flex min-w-0 flex-col">
      <Label htmlFor={id}>{t("agents.form.visibility.label")}</Label>
      <Select
        id={id}
        value={value}
        onValueChange={(next) => {
          if (next === "workspace" || next === "tenant" || next === "public") onChange(next)
        }}
        disabled={disabled}
        aria-describedby={`${id}-hint ${id}-access`}
      >
        <SelectOption value="workspace">{t("agents.form.visibility.workspace")}</SelectOption>
        <SelectOption value="tenant">{t("agents.form.visibility.tenant")}</SelectOption>
        <SelectOption value="public">{t("agents.visibility.public")}</SelectOption>
      </Select>
      <span id={`${id}-hint`} className="mt-1 text-xs text-fg-muted [overflow-wrap:anywhere]">{t(`agents.visibility.${value}Hint`)}</span>
      <span id={`${id}-access`} className="mt-1 text-xs text-fg-muted [overflow-wrap:anywhere]">{t("agents.form.visibility.access")}</span>
    </div>
  )
}
