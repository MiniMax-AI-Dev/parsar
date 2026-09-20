import { Textarea } from "../../components/ui/textarea"
import { jsonObject, type CoreAgentConfig } from "../../lib/core-api"
import { useId, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../components/ui/dialog"
import { Input } from "../../components/ui/input"
import { Label } from "../../components/ui/label"
import { AgentInstructionsField } from "./agents/AgentInstructionsField"
import { AgentVisibilityField } from "./agents/AgentVisibilityField"
import { AgentSaveErrorDialog } from "./agents/AgentSaveErrorDialog"
import type { AgentVisibility } from "../../lib/api-agents"
import type { Agent, CreateAgentRequest, Model, UpdateAgentRequest, UserWorkspace } from "../../lib/api-types"

export type AgentDialogMode = "create" | "edit"
export interface AgentDialogValues {
  agentID?: string
  body: CreateAgentRequest | UpdateAgentRequest
}
export interface CreateAgentDialogProps {
  open: boolean
  mode: AgentDialogMode
  workspaceID: string | null
  workspaceName?: string
  workspaceRole?: UserWorkspace["role"]
  models: Model[]
  agent?: Agent | null
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AgentDialogValues) => void
}

export function CreateAgentDialog(props: CreateAgentDialogProps) {
  const { t } = useTranslation("admin")
  const id = useId()
  const submitRef = useRef<HTMLButtonElement>(null)
  const scopeRef = useRef<HTMLDivElement>(null)
  const [name, setName] = useState(props.agent?.name ?? "")
  const [description, setDescription] = useState(props.agent?.description ?? "")
  const [model, setModel] = useState(String(props.agent?.config?.model ?? ""))
  const [instructions, setInstructions] = useState(String(props.agent?.config?.system_prompt ?? ""))
  const [advanced, setAdvanced] = useState(() => JSON.stringify(Object.fromEntries(Object.entries(props.agent?.config ?? {}).filter(([key]) => ["tools", "service_tier", "multi_agent", "reasoning", "text"].includes(key))), null, 2))
  const [configurationError, setConfigurationError] = useState<string | null>(null)
  const [visibility, setVisibility] = useState<AgentVisibility>(props.agent?.visibility ?? "workspace")
  const valid = name.trim() !== "" && model.trim() !== "" && props.workspaceID !== null
  return <Dialog open={props.open} onOpenChange={props.onOpenChange}>
    <DialogContent ref={scopeRef} className="max-h-[90dvh] overflow-y-auto">
      <form onSubmit={(event) => {
        event.preventDefault()
        if (!valid || props.pending) return
        let extra: Record<string, unknown>
        try {
          extra = jsonObject(advanced)
          if (Object.keys(extra).some(key => !["tools", "service_tier", "multi_agent", "reasoning", "text"].includes(key))) throw new Error(t("core.agentAdvancedHint"))
          setConfigurationError(null)
        } catch (error) { setConfigurationError(error instanceof Error ? error.message : t("core.failed")); return }
        props.onSubmit({
          agentID: props.mode === "edit" ? props.agent?.id : undefined,
          body: { name: name.trim(), description: description.trim(), connector_type: "agents_api", system_prompt: instructions, config: { ...extra, model: model.trim() } as CoreAgentConfig, ...(props.mode === "create" ? { visibility } : {}) },
        })
      }}>
        <DialogHeader>
          <DialogTitle>{t(props.mode === "edit" ? "agents.core.edit" : "agents.core.create")}</DialogTitle>
          <DialogDescription>{t("agents.core.description")}</DialogDescription>
        </DialogHeader>
        <div className="my-4 space-y-4">
          <div><Label htmlFor={`${id}-name`}>{t("agents.core.name")}</Label><Input id={`${id}-name`} value={name} onChange={(event) => setName(event.target.value)} disabled={props.pending} required /></div>
          <div><Label htmlFor={`${id}-description`}>{t("agents.core.summary")}</Label><Input id={`${id}-description`} value={description} onChange={(event) => setDescription(event.target.value)} disabled={props.pending} /></div>
          <div><Label htmlFor={`${id}-model`}>{t("agents.core.model")}</Label><Input id={`${id}-model`} value={model} onChange={(event) => setModel(event.target.value)} disabled={props.pending} required aria-describedby={`${id}-hint`} /><p id={`${id}-hint`} className="mt-1 text-xs text-fg-muted">{t("agents.core.modelHint")}</p></div>
          <AgentInstructionsField value={instructions} onChange={setInstructions} disabled={props.pending} />
          {props.mode === "create" && <AgentVisibilityField value={visibility} onChange={setVisibility} disabled={props.pending} />}
          <details><summary className="cursor-pointer text-sm font-medium">{t("core.advanced")}</summary><p className="my-2 text-xs text-fg-muted">{t("core.agentAdvancedHint")}</p><Textarea aria-label={t("core.advanced")} value={advanced} onChange={event => setAdvanced(event.target.value)} disabled={props.pending} spellCheck={false} /></details>
          {configurationError && <p role="alert">{configurationError}</p>}
          <p className="text-sm text-fg-muted">{t("agents.core.newConversation")}</p>
        </div>
        <DialogFooter><Button type="button" variant="outline" onClick={() => props.onOpenChange(false)} disabled={props.pending}>{t("agents.core.cancel")}</Button><Button ref={submitRef} type="submit" disabled={!valid || props.pending}>{t("agents.core.save")}</Button></DialogFooter>
      </form>
    </DialogContent>
    <AgentSaveErrorDialog error={props.error} message={props.error instanceof Error ? props.error.message : props.error ? t("agents.core.saveFailed") : null} title={t("agents.core.saveFailed")} submitRef={submitRef} scopeRef={scopeRef} />
  </Dialog>
}
