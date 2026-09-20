import { useId, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { apiRequest } from "../../../lib/api-client"
import { catalogKey, catalogPath, type ModelProvider, type CatalogModel } from "../../../lib/model-catalog"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { Label } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"
export type CatalogEdit = { kind: "provider"; provider?: ModelProvider } | { kind: "model"; provider: ModelProvider; model?: CatalogModel } | { kind: "delete"; resource: "models" | "model-providers"; id: string; name: string }
export function CatalogDialog({ workspace, edit, onClose, onSaved }: { workspace: string; edit: CatalogEdit; onClose: () => void; onSaved?: (value: ModelProvider | CatalogModel) => void }) {
  const { t } = useTranslation("admin")
  const id = useId()
  const cache = useQueryClient()
  const current = edit.kind === "provider" ? edit.provider : edit.kind === "model" ? edit.model : undefined
  const [name, setName] = useState(current?.name ?? "")
  const [protocol, setProtocol] = useState(edit.kind === "provider" ? edit.provider?.protocol ?? "anthropic" : "anthropic")
  const [url, setURL] = useState(edit.kind === "provider" ? edit.provider?.base_url ?? "" : "")
  const [key, setKey] = useState("")
  const [modelKey, setModelKey] = useState(edit.kind === "model" ? edit.model?.model_key ?? "" : "")
  const [contextWindow, setContextWindow] = useState(edit.kind === "model" ? edit.model?.context_window ?? 0 : 0)
  const [outputTokens, setOutputTokens] = useState(edit.kind === "model" ? edit.model?.max_output_tokens ?? 0 : 0)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const title = edit.kind === "delete" ? t("catalog.deleteTitle", { name: edit.name }) : t(`catalog.${current ? "edit" : "add"}${edit.kind === "provider" ? "Provider" : "Model"}`)
  return <Dialog open onOpenChange={open => { if (!open && !pending) onClose() }}><DialogContent>
    <form onSubmit={async event => {
      event.preventDefault(); if (pending) return
      setPending(true); setError(null)
      try {
        if (edit.kind === "delete") await apiRequest(catalogPath(workspace, edit.resource, edit.id), { method: "DELETE" })
        else if (edit.kind === "provider") { const saved = await apiRequest<{ provider: ModelProvider }>(catalogPath(workspace, "model-providers", current?.id), { method: current ? "PUT" : "POST", body: { name, protocol, base_url: url, ...(key ? { api_key: key } : {}) } }); onSaved?.(saved.provider) }
        else { const saved = await apiRequest<{ model: CatalogModel }>(catalogPath(workspace, "models", current?.id), { method: current ? "PATCH" : "POST", body: current ? { name } : { name, model_key: modelKey, provider_id: edit.provider.id, context_window: contextWindow, max_output_tokens: outputTokens } }); onSaved?.(saved.model) }
        setKey(""); await cache.invalidateQueries({ queryKey: catalogKey(workspace) }); onClose()
      } catch (err) { setError(err instanceof Error ? err.message : t("core.failed")) }
      finally { setPending(false) }
    }}>
      <DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>{t(edit.kind === "delete" ? "catalog.deleteHint" : edit.kind === "provider" ? "catalog.providerHint" : "catalog.modelHint")}</DialogDescription></DialogHeader>
      {edit.kind !== "delete" && <fieldset disabled={pending} className="my-4 space-y-3">
        <div><Label htmlFor={`${id}-name`}>{t("core.name")}</Label><Input id={`${id}-name`} value={name} onChange={event => setName(event.target.value)} required maxLength={256} /></div>
        {edit.kind === "provider" ? <>
          <div><Label htmlFor={`${id}-protocol`}>{t("catalog.protocol")}</Label><Select id={`${id}-protocol`} value={protocol} onValueChange={value => setProtocol(value as ModelProvider["protocol"])} disabled={pending}><SelectOption value="anthropic">Anthropic Messages · Claude Code / MiniMax Code</SelectOption><SelectOption value="responses">OpenAI Responses · Codex</SelectOption></Select></div>
          <div><Label htmlFor={`${id}-url`}>Base URL</Label><Input id={`${id}-url`} type="url" placeholder="https://api.example.com/anthropic" value={url} onChange={event => setURL(event.target.value)} required /></div>
          <div><Label htmlFor={`${id}-key`}>API Key</Label><Input id={`${id}-key`} type="password" autoComplete="new-password" value={key} onChange={event => setKey(event.target.value)} required={!current} placeholder={t(current ? "catalog.keyUnchanged" : "catalog.keyRequired")} /></div>
        </> : <>
          <div><Label htmlFor={`${id}-model`}>{t("catalog.modelKey")}</Label><Input id={`${id}-model`} value={modelKey} onChange={event => setModelKey(event.target.value)} required disabled={!!current || pending} maxLength={256} /></div>
          <p className="text-xs text-fg-muted">{t("catalog.limitsHint")}</p>
          <div><Label htmlFor={`${id}-context`}>{t("catalog.contextWindow")}</Label><Input id={`${id}-context`} type="number" min={0} max={2147483647} value={contextWindow} onChange={event => setContextWindow(Number(event.target.value))} disabled={!!current || pending} /></div>
          <div><Label htmlFor={`${id}-output`}>{t("catalog.outputTokens")}</Label><Input id={`${id}-output`} type="number" min={0} max={contextWindow} value={outputTokens} onChange={event => setOutputTokens(Number(event.target.value))} disabled={!!current || pending} /></div>
        </>}
      </fieldset>}
      {error && <p role="alert" className="my-3 text-sm text-danger">{error}</p>}
      <DialogFooter><Button type="button" variant="outline" disabled={pending} onClick={onClose}>{t("core.cancel")}</Button><Button type="submit" variant={edit.kind === "delete" ? "destructive" : "default"} disabled={pending}>{t(edit.kind === "delete" ? "core.delete" : "agents.core.save")}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
