import { useAgentCloneCredentials } from "./agents/useAgentCloneCredentials"
import { AgentCloneCredentials } from "./agents/AgentCloneCredentials"
import { useSecrets } from "../../lib/api-secrets"
import { AgentResourceFields } from "./agents/AgentResourceFields"
import { AgentConnectionsField } from "./agents/AgentConnectionsField"
import { AgentModelCredentialField, type AgentModelCredential } from "./agents/AgentModelCredentialField"
import { useAgentResourceDraft } from "./agents/useAgentResourceDraft"
import { Textarea } from "../../components/ui/textarea"
import { jsonObject, coreExecutionDefaults, type CoreAgentConfig } from "../../lib/core-api"
import { useId, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../components/ui/dialog"
import { Input } from "../../components/ui/input"
import { Label } from "../../components/ui/label"
import { useModelCatalog, supportsCatalogModel } from "../../lib/model-catalog"
import { AgentModelField } from "./agents/AgentModelField"
import { AgentExecutionFields } from "./agents/AgentExecutionFields"
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
  const [execution, setExecution] = useState(() => coreExecutionDefaults(props.agent?.config))
  const [name, setName] = useState(props.agent?.name ?? "")
  const [description, setDescription] = useState(props.agent?.description ?? "")
  const [modelID, setModelID] = useState(String(props.agent?.config?.model_id ?? ""))
  const isClone = props.mode === "create" && !!props.agent
  const resources = useAgentResourceDraft(props.workspaceID, props.agent?.id)
  const cloneSecrets = useSecrets(isClone ? props.workspaceID : null)
  const cloneCredentials = useAgentCloneCredentials(isClone ? props.agent!.id : null, props.workspaceID, props.agent?.config ?? {}, resources.bindings,
    resources.selection?.map(item => item.capability) ?? [], resources.selection?.map(item => item.capability.id) ?? [],
    Object.fromEntries((resources.selection ?? []).map(item => [item.capability.id, { pinningMode: item.pinning_mode === "latest" ? "latest" : "pinned", versionID: item.capability_version_id }])), cloneSecrets.data?.secrets ?? [])
  const [modelCredential, setModelCredential] = useState<AgentModelCredential | undefined>(() => {
    const saved = props.agent?.config?.model_credential_binding as AgentModelCredential | undefined
    return isClone && saved?.source === "personal" ? { kind: saved.kind, source: "shared" } : saved
  })
  const catalog = useModelCatalog(props.workspaceID)
  const model = catalog.data?.models.find(row => row.id === modelID)
  const [instructions, setInstructions] = useState(String(props.agent?.config?.system_prompt ?? ""))
  const [advanced, setAdvanced] = useState(() => JSON.stringify(Object.fromEntries(Object.entries(props.agent?.config ?? {}).filter(([key]) => ["tools", "service_tier", "multi_agent", "reasoning", "text"].includes(key))), null, 2))
  const [configurationError, setConfigurationError] = useState<string | null>(null)
  const [visibility, setVisibility] = useState<AgentVisibility>(props.agent?.visibility ?? "workspace")
  const cloneReady = !isClone || (cloneCredentials.ready && cloneCredentials.valid && !cloneSecrets.error && !cloneSecrets.isLoading && (!modelCredential || (modelCredential.source === "shared" && !!modelCredential.secret_id)))
  const valid = cloneReady && !resources.loading && !resources.error && name.trim() !== "" && !!model && supportsCatalogModel(model, execution.harness) && execution.environment.type === "openai_hosted" && execution.harness !== "" && props.workspaceID !== null
  return <Dialog open={props.open} onOpenChange={props.onOpenChange}>
    <DialogContent ref={scopeRef} className="max-h-[90dvh] overflow-y-auto">
      <form onSubmit={(event) => {
        if (event.target !== event.currentTarget) return
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
          body: { resource_bindings: resources.selection?.map(({ capability, ...binding }) => ({ ...binding, ...(isClone ? { configuration: cloneCredentials.rows.find(row => row.capabilityID === capability.id)?.configuration ?? {} } : {}) })), name: name.trim(), description: description.trim(), connector_type: "agents_api", system_prompt: instructions, config: { ...extra, ...(!isClone && props.agent?.config?.credential_bindings ? { credential_bindings: props.agent.config.credential_bindings } : {}), ...(modelCredential ? { model_credential_binding: modelCredential } : {}), model: model!.model_key, model_id: modelID, environment: execution.environment, x_agents_core: { harness: execution.harness } } as CoreAgentConfig, ...(props.mode === "create" ? { visibility } : {}) },
        })
      }}>
        <DialogHeader>
          <DialogTitle>{t(props.mode === "edit" ? "agents.core.edit" : "agents.core.create")}</DialogTitle>
          <DialogDescription>{t("agents.core.description")}</DialogDescription>
        </DialogHeader>
        <div className="my-4 space-y-4">
          <div><Label htmlFor={`${id}-name`}>{t("agents.core.name")}</Label><Input id={`${id}-name`} value={name} onChange={(event) => setName(event.target.value)} disabled={props.pending} required /></div>
          <div><Label htmlFor={`${id}-description`}>{t("agents.core.summary")}</Label><Input id={`${id}-description`} value={description} onChange={(event) => setDescription(event.target.value)} disabled={props.pending} /></div>
          <AgentModelField workspaceID={props.workspaceID} value={modelID} legacyModel={String(props.agent?.config?.model ?? "")} harness={execution.harness} onChange={setModelID} disabled={props.pending} />
          <AgentModelCredentialField workspaceID={props.workspaceID} value={modelCredential} onChange={setModelCredential} publicAgent={visibility === "public" || isClone} />
          <AgentExecutionFields workspaceID={props.workspaceID} harness={execution.harness} environment={execution.environment} onHarnessChange={harness => setExecution(value => ({ ...value, harness }))} onEnvironmentChange={environment => setExecution(value => ({ ...value, environment }))} disabled={props.pending} />
          {model && (!supportsCatalogModel(model, execution.harness) || execution.environment.type !== "openai_hosted") && <p role="alert" className="text-sm text-danger">{t("catalog.incompatible")}</p>}
          {resources.selection?.some(item => item.capability.type === "skill") && execution.environment.type === "openai_hosted" && execution.environment.environment_template_id && <p role="status" className="text-sm text-fg-muted">{t("agentResources.templateSkillsPending")}</p>}
          {isClone && <p className="text-sm text-fg-muted">{t("agents.form.clone.credentials")}</p>}
          <AgentResourceFields hideCredentials={isClone} workspaceID={props.workspaceID} draft={resources} defaults={props.agent?.config} publicAgent={visibility === "public"} disabled={props.pending} />
          {isClone && <AgentCloneCredentials credentials={cloneCredentials} workspaceID={props.workspaceID} failed={!!cloneSecrets.error || cloneCredentials.failed} fetching={cloneSecrets.isFetching || !cloneCredentials.ready} onRefresh={() => { void cloneSecrets.refetch(); cloneCredentials.retry() }} />}
          <AgentConnectionsField workspaceID={props.workspaceID} agent={props.mode === "edit" ? props.agent : undefined} />
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
