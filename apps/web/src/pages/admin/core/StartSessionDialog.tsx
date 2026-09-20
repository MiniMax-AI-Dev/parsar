import { useId, useState } from "react"
import { useTranslation } from "react-i18next"
import { useAgents } from "../../../lib/api-agents"
import { createConversation } from "../../../lib/api-conversations"
import { useAdminView } from "../../../lib/admin-router"
import { useCoreTemplates, type CoreEnvironment } from "../../../lib/core-api"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogFooter } from "../../../components/ui/dialog"
import { Select, SelectOption } from "../../../components/ui/select"
import { Input } from "../../../components/ui/input"
import { Label } from "../../../components/ui/label"

export function StartSessionDialog({ workspaceID, initialAgentID, onClose }: { workspaceID: string; initialAgentID?: string; onClose: () => void }) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const id = useId()
  const agents = useAgents(workspaceID)
  const [agentID, setAgentID] = useState(initialAgentID ?? "")
  const [kind, setKind] = useState<CoreEnvironment["type"]>("openai_hosted")
  const [templateID, setTemplateID] = useState("")
  const [directory, setDirectory] = useState("/workspace")
  const [title, setTitle] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const templates = useCoreTemplates(workspaceID, kind === "openai_hosted")
  const list = agents.data?.agents.filter(agent => agent.status === "active" && agent.connector_type === "agents_api") ?? []
  const selected = agentID || list[0]?.id || ""
  return <Dialog open onOpenChange={open => { if (!open && !pending) onClose() }}>
    <DialogContent className="max-h-[90dvh] overflow-y-auto">
      <form className="space-y-4" onSubmit={async event => {
        event.preventDefault()
        if (!selected || pending) return
        setPending(true); setError(null)
        const environment: CoreEnvironment = kind === "openai_hosted" ? { type: kind, ...(templateID ? { environment_template_id: templateID } : {}) } : kind === "self_hosted" ? { type: kind, workspace_directory: directory } : { type: kind }
        try {
          const session = await createConversation(workspaceID, { title: title.trim() || list.find(agent => agent.id === selected)?.name || "", agent_id: selected, surface: "web", form: "thread", environment })
          onClose(); navigate("conversations", { id: session.id, focus: "compose" })
        } catch (error) { setError(error instanceof Error ? error.message : t("core.failed")) }
        finally { setPending(false) }
      }}>
        <DialogHeader><DialogTitle>{t("core.startSession")}</DialogTitle><DialogDescription>{t("core.sessionHint")}</DialogDescription></DialogHeader>
        <div><Label htmlFor={`${id}-agent`}>{t("core.agent")}</Label><Select id={`${id}-agent`} value={selected} onValueChange={setAgentID} disabled={pending}>{list.map(agent => <SelectOption key={agent.id} value={agent.id}>{agent.name}</SelectOption>)}</Select></div>
        {agents.error instanceof Error && <p role="alert">{agents.error.message}</p>}
        <div><Label htmlFor={`${id}-title`}>{t("core.sessionTitle")}</Label><Input id={`${id}-title`} value={title} onChange={event => setTitle(event.target.value)} disabled={pending} /></div>
        <fieldset disabled={pending} className="space-y-2"><legend className="mb-2 text-sm font-medium">{t("core.environment")}</legend>
          {(["openai_hosted", "self_hosted", "none"] as const).map(value => <label key={value} className="flex items-start gap-2 rounded-md border border-line p-3">
            <input className="mt-1" type="radio" name={`${id}-environment`} checked={kind === value} onChange={() => setKind(value)} />
            <span><span className="block font-medium">{t(`core.environmentTypes.${value}`)}</span><span className="block text-xs text-fg-muted">{t(`core.environmentHints.${value}`)}</span></span>
          </label>)}
        </fieldset>
        {kind === "openai_hosted" && <div><Label htmlFor={`${id}-template`}>{t("core.template")}</Label><Select id={`${id}-template`} value={templateID} onValueChange={setTemplateID} disabled={pending}>
          <SelectOption value="">{t("core.defaultEnvironment")}</SelectOption>
          {templates.data?.pages.flatMap(page => page.data).map(template => <SelectOption key={template.id} value={template.id}>{template.name || template.id}</SelectOption>)}
        </Select>{templates.hasNextPage && <Button type="button" variant="ghost" disabled={templates.isFetchingNextPage} onClick={() => void templates.fetchNextPage()}>{t("core.loadMore")}</Button>}
        {templates.error && <p className="mt-2 text-sm text-fg-muted" role="alert">{templates.error.message}</p>}</div>}
        {kind === "self_hosted" && <div><Label htmlFor={`${id}-directory`}>{t("core.workspaceDirectory")}</Label><Input id={`${id}-directory`} value={directory} onChange={event => setDirectory(event.target.value)} disabled={pending} required pattern="/.*" /><p className="mt-2 text-xs text-fg-muted">{t("core.selfHostedPending")}</p></div>}
        {error && <p role="alert" className="text-sm text-fg-muted">{error}</p>}
        <DialogFooter><Button type="button" variant="outline" onClick={onClose} disabled={pending}>{t("core.cancel")}</Button><Button type="submit" disabled={pending || !selected || kind === "self_hosted"}>{pending ? t("core.saving") : t("core.startSession")}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
}
