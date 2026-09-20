import { EnvironmentTemplateDialog } from "../core/EnvironmentTemplateDialog"
import { useId, useState } from "react"
import { useTranslation } from "react-i18next"
import { useCoreTemplates, type CoreEnvironment, type CoreHarness } from "../../../lib/core-api"
import { Button } from "../../../components/ui/button"
import { Label } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"

export function AgentExecutionFields({ workspaceID, harness, environment, onHarnessChange, onEnvironmentChange, disabled }: {
  workspaceID: string | null
  harness: CoreHarness | ""
  environment: CoreEnvironment
  onHarnessChange: (value: CoreHarness) => void
  onEnvironmentChange: (value: CoreEnvironment) => void
  disabled: boolean
}) {
  const { t } = useTranslation("admin")
  const id = useId()
  const [creating, setCreating] = useState(false)
  const templates = useCoreTemplates(workspaceID, environment.type === "openai_hosted")
  return <fieldset className="space-y-3" disabled={disabled}>
    <legend className="mb-2 text-sm font-medium">{t("core.executionDefaults")}</legend>
    <div><Label htmlFor={`${id}-harness`}>Harness</Label>
      <Select id={`${id}-harness`} value={harness} onValueChange={value => onHarnessChange(value as CoreHarness)} disabled={disabled} aria-required="true">
        {!harness && <SelectOption value="" disabled>{t("core.selectHarness")}</SelectOption>}
        <SelectOption value="codex">Codex</SelectOption>
        <SelectOption value="claude_sdk">Claude Code</SelectOption>
        <SelectOption value="mcode">MiniMax Code</SelectOption>
      </Select><p className="mt-1 text-xs text-fg-muted">{t("core.harnessHint")}</p>
    </div>
    <div><Label htmlFor={`${id}-environment`}>{t("agentResources.sandbox")}</Label>
      <Select id={`${id}-environment`} value={environment.type} onValueChange={value => onEnvironmentChange(value === "none" ? { type: "none" } : { type: "openai_hosted" })} disabled={disabled}>
        <SelectOption value="openai_hosted">{t("core.environmentTypes.openai_hosted")}</SelectOption>
        <SelectOption value="none">{t("core.environmentTypes.none")}</SelectOption>
        <SelectOption value="self_hosted" disabled>{t("core.environmentTypes.self_hosted")} · {t("core.awaitingCore")}</SelectOption>
      </Select>
    </div>
    {environment.type === "openai_hosted" && <div>
      <Label htmlFor={`${id}-template`}>{t("core.template")}</Label>
      <Select id={`${id}-template`} value={environment.environment_template_id ?? ""} onValueChange={value => onEnvironmentChange({ type: "openai_hosted", ...(value ? { environment_template_id: value } : {}) })} disabled={disabled}>
        <SelectOption value="">{t("core.defaultEnvironment")}</SelectOption>
        {environment.environment_template_id && !templates.data?.pages.some(page => page.data.some(template => template.id === environment.environment_template_id)) && <SelectOption value={environment.environment_template_id}>{environment.environment_template_id}</SelectOption>}
        {templates.data?.pages.flatMap(page => page.data).map(template => <SelectOption key={template.id} value={template.id}>{template.name || template.id}</SelectOption>)}
      </Select>
      <Button type="button" variant="outline" disabled={disabled} onClick={() => setCreating(true)}>{t("core.createTemplate")}</Button>
      {workspaceID && creating && <EnvironmentTemplateDialog workspaceID={workspaceID} onClose={() => setCreating(false)} onSaved={template => onEnvironmentChange({ type: "openai_hosted", environment_template_id: template.id })} />}
      {templates.hasNextPage && <Button type="button" variant="ghost" disabled={disabled || templates.isFetchingNextPage} onClick={() => void templates.fetchNextPage()}>{t("core.loadMore")}</Button>}
      {templates.error && <p role="alert" className="mt-1 text-sm text-danger">{templates.error.message}</p>}
    </div>}
    <p className="text-xs text-fg-muted">{t("core.executionDefaultsHint")}</p>
  </fieldset>
}
