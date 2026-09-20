import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { Select, SelectOption } from "../../../components/ui/select"
import { useAgentResourceVersions } from "./useAgentResourceVersions"
import { ImportCapabilityDialog } from "../capabilities/ImportCapabilityDialog"
import { AgentCredentialField } from "./AgentCredentialField"
import type { AgentResourceSelection, useAgentResourceDraft } from "./useAgentResourceDraft"

export function AgentResourceFields({ workspaceID, draft, defaults, publicAgent, disabled, hideCredentials = false }: {
  workspaceID: string | null; draft: ReturnType<typeof useAgentResourceDraft>; defaults?: Record<string, unknown>; publicAgent: boolean; disabled: boolean; hideCredentials?: boolean
}) {
  const { t } = useTranslation("admin")
  const [adding, setAdding] = useState(false)
  const supported = ["skill", "mcp", "system_prompt", "knowledge"]
  return <fieldset disabled={disabled} className="space-y-3 border-t border-line pt-4">
    <legend className="text-sm font-medium">{t("agentResources.title")}</legend>
    <p className="text-xs text-fg-muted">{t("agentResources.hint")}</p>
    {draft.loading && <p role="status">{t("agentResources.loading")}</p>}
    {Boolean(draft.error) && <p role="alert">{t("agentResources.loadFailed")} <Button type="button" variant="ghost" onClick={draft.retry}>{t("agentResources.retry")}</Button></p>}
    <Select aria-label={t("agentResources.select")} value="" onValueChange={id => { const capability = draft.available.find(item => item.id === id); if (capability) draft.add(capability) }} disabled={disabled || draft.loading || !!draft.error}>
      <SelectOption value="">{t("agentResources.select")}</SelectOption>
      {draft.available.filter(item => !draft.selection?.some(selected => selected.capability.id === item.id)).map(item => <SelectOption key={item.id} value={item.id} disabled={item.status !== "active" || !(item.pinned_version_id ?? item.latest_version_id) || !supported.includes(item.type)}>{item.name} · {item.type}{!supported.includes(item.type) ? ` · ${t("agentResources.unsupported")}` : ""}</SelectOption>)}
    </Select>
    {draft.selection?.map((item, index) => <ResourceRow key={item.capability.id} workspaceID={workspaceID} item={item} defaults={defaults} publicAgent={publicAgent} hideCredentials={hideCredentials} onChange={value => draft.setSelection(current => current!.map((row, i) => i === index ? value : row))} onRemove={() => draft.setSelection(current => current!.filter((_, i) => i !== index))} />)}
    <Button type="button" variant="outline" onClick={() => setAdding(true)}>{t("agentResources.add")}</Button>
    <ImportCapabilityDialog workspaceID={workspaceID} open={adding} onOpenChange={setAdding} onCreated={capability => { draft.add(capability); setAdding(false) }} />
  </fieldset>
}

function ResourceRow({ workspaceID, item, defaults, publicAgent, hideCredentials, onChange, onRemove }: {
  workspaceID: string | null; item: AgentResourceSelection; defaults?: Record<string, unknown>; publicAgent: boolean; hideCredentials: boolean; onChange: (value: AgentResourceSelection) => void; onRemove: () => void
}) {
  const { t } = useTranslation("admin")
  const versions = useAgentResourceVersions(workspaceID, item)
  const latestVersionID = "latestVersionID" in versions ? versions.latestVersionID : item.capability.latest_version_id
  const version = versions.versions.find(row => row.id === (item.pinning_mode === "latest" ? latestVersionID : item.capability_version_id))
  const required = version?.required_credentials ?? item.capability.required_credentials ?? []
  const selectVersion = (value: string) => {
    const versionID = value === "latest" ? latestVersionID ?? item.capability_version_id : value
    const selectedVersion = versions.versions.find(row => row.id === versionID)
    if (!selectedVersion) return
    const kinds = new Set((selectedVersion.required_credentials ?? []).map(row => row.kind))
    const configuration = { ...item.configuration }
    if (configuration.credential_bindings) {
      configuration.credential_bindings = Object.fromEntries(Object.entries(configuration.credential_bindings as Record<string, unknown>).filter(([kind]) => kinds.has(kind)))
    }
    onChange({ ...item, pinning_mode: value === "latest" ? "latest" : "pinned", capability_version_id: versionID, configuration })
  }
  const source = version?.source_payload as { catalog_id?: string } | undefined
  return <div className="space-y-3 rounded-md border border-line p-3">
    {item.capability.type === "mcp" && <p role="status" className="text-sm text-fg-muted">{t("agentResources.mcpPending")}</p>}
    {item.capability.type === "skill" && required.length > 0 && <p role="status" className="text-sm text-fg-muted">{t("agentResources.skillCredentialsPending")}</p>}
    <div className="flex items-center justify-between gap-2"><span className="min-w-0 break-words text-sm font-medium">{item.capability.name} <span className="font-normal text-fg-muted">· {item.capability.type}</span></span><Button type="button" variant="ghost" onClick={onRemove}>{t("agentResources.remove")}</Button></div>
    {item.capability.status !== "active" && <p role="alert">{t("agentResources.unavailable")}</p>}
    <Select aria-label={t("agentResources.version", { name: item.capability.name })} value={item.pinning_mode === "latest" ? "latest" : item.capability_version_id} disabled={versions.loading || !!versions.error} onValueChange={selectVersion}>
      <SelectOption value="latest" disabled={!latestVersionID}>{t("agentResources.latest")}</SelectOption>
      {!versions.versions.some(row => row.id === item.capability_version_id) && <SelectOption value={item.capability_version_id}>{item.capability_version_id}</SelectOption>}
      {versions.versions.map(row => <SelectOption key={row.id} value={row.id}>{row.version}</SelectOption>)}
    </Select>
    {Boolean(versions.error) && <p role="alert">{t("agentResources.loadFailed")}</p>}
    {!hideCredentials && required.filter(row => row.required).map(row => <AgentCredentialField key={row.kind} workspaceID={workspaceID} kind={row.kind} configuration={item.configuration ?? {}} defaults={defaults} onChange={configuration => onChange({ ...item, configuration })} publicAgent={publicAgent} catalogID={source?.catalog_id} />)}
  </div>
}
