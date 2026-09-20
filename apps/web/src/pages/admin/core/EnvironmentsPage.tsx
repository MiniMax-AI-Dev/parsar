import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { Box, KeyRound } from "lucide-react"
import { AdminLayout } from "../../../components/layout/AdminLayout"
import { PageHeader } from "../../../components/layout/PageHeader"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { EmptyState } from "../../../components/ui/empty-state"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { useWorkspaceId } from "../../../lib/workspace"
import { useMyWorkspaces } from "../../../lib/api-workspaces"
import { coreTemplateKey, deleteCoreTemplate, useCoreTemplates, type CoreTemplate } from "../../../lib/core-api"
import { EnvironmentTemplateDialog } from "./EnvironmentTemplateDialog"

export function EnvironmentsPage() {
  const { t, i18n } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const workspaces = useMyWorkspaces()
  const role = workspaces.data?.workspaces.find(workspace => workspace.id === workspaceID)?.role
  const canManage = role === "owner" || role === "admin"
  const [tab, setTab] = useState<"templates" | "keys">("templates")
  const [search, setSearch] = useState("")
  const [editing, setEditing] = useState<{ template?: CoreTemplate } | null>(null)
  const [deleting, setDeleting] = useState<CoreTemplate | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const templates = useCoreTemplates(workspaceID, tab === "templates")
  const queryClient = useQueryClient()
  const rows = templates.data?.pages.flatMap(page => page.data).filter(template => `${template.name ?? ""} ${template.id}`.toLocaleLowerCase().includes(search.toLocaleLowerCase())) ?? []
  return <AdminLayout activeMenu="environments">
    <PageHeader title={t("core.environments")} action={canManage && tab === "templates" ? <Button disabled={!workspaceID} onClick={() => setEditing({})}>{t("core.createTemplate")}</Button> : undefined} />
    <div className="mb-5 flex flex-wrap gap-2"><Input className="min-w-0 max-w-md" type="search" aria-label={t("core.searchTemplates")} placeholder={t("core.searchTemplates")} value={search} onChange={event => setSearch(event.target.value)} disabled={tab === "keys"} />
      {(["templates", "keys"] as const).map(value => <Button key={value} variant={tab === value ? "secondary" : "ghost"} aria-pressed={tab === value} onClick={() => setTab(value)}>{t(`core.${value}`)}</Button>)}</div>
    {tab === "keys" ? <EmptyState icon={KeyRound} title={t("core.keys")} description={t("core.keysUnavailable")} /> : !workspaceID ? <p>{t("core.selectWorkspace")}</p> : templates.isLoading ? <p role="status">{t("core.loading")}</p> : templates.error ? <div role="alert" className="space-y-3"><p>{templates.error.message}</p><Button variant="outline" onClick={() => void templates.refetch()}>{t("core.retry")}</Button></div> : rows.length === 0 ? <EmptyState icon={Box} title={t(search ? "core.noResults" : "core.noTemplates")} description={t("core.templateHint")} action={canManage && !search ? <Button variant="outline" onClick={() => setEditing({})}>{t("core.createTemplate")}</Button> : undefined} /> : <div className="overflow-x-auto"><table className="w-full text-left text-sm">
      <thead className="text-fg-muted"><tr>{(["name", "templateID", "network", "created", "actions"] as const).map(key => <th key={key} scope="col" className="border-b border-line px-3 py-3 font-normal">{t(`core.${key}`)}</th>)}</tr></thead>
      <tbody>{rows.map(template => <tr key={template.id} className="border-b border-line"><td className="px-3 py-4">{template.name || t("core.unnamed")}</td><td className="px-3 py-4 font-mono text-xs">{template.id}</td><td className="px-3 py-4">{template.network?.access ? t(`core.networkModes.${template.network.access}`) : "—"}</td><td className="px-3 py-4">{new Date(template.created_at * 1000).toLocaleDateString(i18n.language)}</td><td className="px-3 py-4">{canManage && <div className="flex gap-2"><Button size="sm" variant="ghost" onClick={() => setEditing({ template })}>{t("core.edit")}</Button><Button size="sm" variant="ghost" onClick={() => { setError(null); setDeleting(template) }}>{t("core.delete")}</Button></div>}</td></tr>)}</tbody>
    </table></div>}
    {tab === "templates" && templates.hasNextPage && <Button className="mt-4" variant="outline" disabled={templates.isFetchingNextPage} onClick={() => void templates.fetchNextPage()}>{t("core.loadMore")}</Button>}
    {editing && workspaceID && canManage && <EnvironmentTemplateDialog key={`${workspaceID}:${editing.template?.id ?? 'new'}`} workspaceID={workspaceID} template={editing.template} onClose={() => setEditing(null)} />}
    <Dialog open={!!deleting} onOpenChange={open => { if (!open && !pending) setDeleting(null) }}><DialogContent><DialogHeader><DialogTitle>{t("core.deleteTemplate")}</DialogTitle><DialogDescription>{t("core.deleteTemplateHint", { name: deleting?.name || deleting?.id })}</DialogDescription></DialogHeader>{error && <p role="alert">{error}</p>}<DialogFooter><Button variant="outline" disabled={pending} onClick={() => setDeleting(null)}>{t("core.cancel")}</Button><Button variant="destructive" disabled={pending} onClick={async () => {
      if (!workspaceID || !deleting) return
      setPending(true)
      try { await deleteCoreTemplate(workspaceID, deleting.id); await queryClient.invalidateQueries({ queryKey: coreTemplateKey(workspaceID) }); setDeleting(null) }
      catch (error) { setError(error instanceof Error ? error.message : t("core.failed")) }
      finally { setPending(false) }
    }}>{t("core.delete")}</Button></DialogFooter></DialogContent></Dialog>
  </AdminLayout>
}
