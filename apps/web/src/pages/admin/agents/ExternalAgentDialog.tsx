import { useId, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { ErrorDialog } from "../../../components/ui/error-dialog"
import { Input } from "../../../components/ui/input"
import { Label } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"
import { Textarea } from "../../../components/ui/textarea"
import { useCreateSecret, useSecrets } from "../../../lib/api-secrets"
import type { AgentVisibility } from "../../../lib/api-agents"
import { httpAgentConfig, validHTTPAgentEndpoint } from "../../../lib/http-agent"
import type { CreateAgentDialogProps, AgentFormDraft } from "../CreateAgentDialog"
import { AgentVisibilityField } from "./AgentVisibilityField"

export function ExternalAgentDialog({ agent, mode, workspaceID, pending, error, onSubmit, onOpenChange, onBack, initialDraft }: CreateAgentDialogProps & { onBack?: (draft: AgentFormDraft) => void }) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const id = useId()
  const scope = useRef<HTMLDivElement>(null)
  const submit = useRef<HTMLButtonElement>(null)
  const initial = httpAgentConfig(agent?.config)
  const [name, setName] = useState(initialDraft?.name ?? (agent ? `${agent.name}${mode === "create" ? " (Copy)" : ""}` : ""))
  const [description, setDescription] = useState(initialDraft?.description ?? agent?.description ?? "")
  const [endpoint, setEndpoint] = useState(initial.endpoint)
  const [endpointTouched, setEndpointTouched] = useState(false)
  const [instructions, setInstructions] = useState(initialDraft?.systemPrompt ?? String(agent?.config?.system_prompt ?? ""))
  const [visibility, setVisibility] = useState<AgentVisibility>(initialDraft?.visibility ?? "workspace")
  const [credential, setCredential] = useState(initial.secretID || "none")
  const [token, setToken] = useState("")
  const [localError, setLocalError] = useState<unknown>(null)
  const [dismissedError, setDismissedError] = useState<unknown>(null)
  const createSecret = useCreateSecret(workspaceID)
  const secrets = useSecrets(workspaceID)
  const options = (secrets.data?.secrets ?? []).filter((secret) => secret.management_workspace_id === workspaceID && secret.kind === "http_agent" && secret.provider === "http_agent" && secret.auth_type === "bearer" && secret.status === "active")
  const busy = pending || createSecret.isPending
  const failure = localError || error
  const errorOpen = Boolean(failure && failure !== dismissedError)
  const field = (key: string) => `${id}-${key}`
  const ready = name.trim() && validHTTPAgentEndpoint(endpoint) && (credential !== "new" || token.trim())

  async function save() {
    setLocalError(null)
    setDismissedError(null)
    try {
      let secretID = credential === "none" ? "" : credential
      if (credential === "new") {
        const secret = await createSecret.mutateAsync({ body: { name: `${name.trim()} HTTP`, kind: "http_agent", provider: "http_agent", auth_type: "bearer", payload: { token: token.trim() } } })
        secretID = secret.id
        setCredential(secret.id)
        setToken("")
      }
      onSubmit({ agentID: agent?.id, body: {
        name: name.trim(), description: description.trim(), connector_type: "http", system_prompt: instructions,
        ...(mode === "create" ? { visibility } : {}),
        config: { http: { endpoint: endpoint.trim(), secret_id: secretID } },
      } })
    } catch (cause) { setLocalError(cause) }
  }

  return <>
    <Dialog open onOpenChange={(open) => { if (!busy && !errorOpen) onOpenChange(open) }}>
      <DialogContent ref={scope} onInteractOutside={(event) => { if (busy || errorOpen) event.preventDefault() }} className="w-[calc(100%-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto overflow-x-hidden sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t(mode === "edit" ? "agents.http.edit" : "agents.http.create")}</DialogTitle>
          <DialogDescription>{t("agents.http.ownership")}</DialogDescription>
        </DialogHeader>
        <form className="flex min-w-0 flex-col gap-4" onSubmit={(event) => { event.preventDefault(); if (ready && !busy) void save() }}>
          <div><Label htmlFor={field("name")}>{t("agents.form.fields.name")}</Label><Input id={field("name")} required autoFocus value={name} disabled={busy} onChange={(e) => setName(e.target.value)} /></div>
          <div><Label htmlFor={field("description")}>{t("agents.form.fields.description")}</Label><Input id={field("description")} value={description} disabled={busy} onChange={(e) => setDescription(e.target.value)} /></div>
          <div>
            <Label htmlFor={field("endpoint")}>{t("agents.http.endpoint")}</Label>
            <Input id={field("endpoint")} type="url" required placeholder="https://agent.example.com/invoke" value={endpoint} disabled={busy} onChange={(e) => setEndpoint(e.target.value)} onBlur={() => setEndpointTouched(true)} aria-invalid={endpointTouched && !validHTTPAgentEndpoint(endpoint)} aria-describedby={field("endpoint-hint")} />
            <p id={field("endpoint-hint")} className="mt-1 text-xs text-fg-muted">{endpointTouched && !validHTTPAgentEndpoint(endpoint) ? t("agents.http.invalidEndpoint") : t("agents.http.endpointHint")}</p>
          </div>
          <div>
            <Label htmlFor={field("credential")}>{t("agents.http.authentication")}</Label>
            <Select id={field("credential")} value={credential} disabled={busy} onValueChange={setCredential}>
              <SelectOption value="none">{t("agents.http.noAuth")}</SelectOption>
              <SelectOption value="new">{t("agents.http.newToken")}</SelectOption>
              {credential !== "none" && credential !== "new" && !options.some((s) => s.id === credential) && <SelectOption value={credential}>{t("agents.http.savedCredential")}</SelectOption>}
              {options.map((secret) => <SelectOption key={secret.id} value={secret.id}>{secret.name}</SelectOption>)}
            </Select>
            {credential === "new" && <div className="mt-3"><Label htmlFor={field("token")}>{t("agents.http.token")}</Label><Input id={field("token")} type="password" autoComplete="new-password" required value={token} disabled={busy} onChange={(e) => setToken(e.target.value)} /></div>}
            <p className="mt-1 text-xs text-fg-muted">{t("agents.http.authHint")}</p>
          </div>
          <div><Label htmlFor={field("instructions")}>{t("agents.form.instructions.label")}</Label><Textarea id={field("instructions")} rows={3} value={instructions} disabled={busy} onChange={(e) => setInstructions(e.target.value)} /><p className="mt-1 text-xs text-fg-muted">{t("agents.http.instructionsHint")}</p></div>
          {mode === "create" && <AgentVisibilityField value={visibility} onChange={setVisibility} disabled={busy} />}
          <DialogFooter>
            {onBack && <Button type="button" variant="outline" disabled={busy} onClick={() => onBack({ name, description, systemPrompt: instructions, visibility })}>{t("agents.http.back")}</Button>}
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>{tc("actions.cancel")}</Button>
            <Button ref={submit} type="submit" variant="default" disabled={!ready || busy}>{busy ? t("agents.http.saving") : tc("actions.save")}</Button>
          </DialogFooter>
        </form>
    {errorOpen ? <ErrorDialog title={t("agents.http.saveFailed")} message={failure instanceof Error ? failure.message : String(failure)} onClose={() => setDismissedError(failure)} onRestoreFocus={() => submit.current?.focus()} focusScopeRef={scope} /> : null}
      </DialogContent>
    </Dialog>
  </>
}
