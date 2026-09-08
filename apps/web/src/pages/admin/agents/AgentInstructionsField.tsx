import { useId } from "react"
import { useTranslation } from "react-i18next"
import { Label } from "../../../components/ui/label"
import { Textarea } from "../../../components/ui/textarea"

export function AgentInstructionsField({ value, onChange, disabled }: {
  value: string
  onChange: (value: string) => void
  disabled: boolean
}) {
  const { t } = useTranslation("admin")
  const id = useId()
  return (
    <div className="flex flex-col">
      <Label htmlFor={id}>{t("agents.form.instructions.label")}</Label>
      <Textarea
        id={id}
        aria-describedby={`${id}-hint`}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        rows={4}
        disabled={disabled}
      />
      <span id={`${id}-hint`} className="mt-1 text-xs text-fg-muted">{t("agents.form.instructions.hint")}</span>
    </div>
  )
}
