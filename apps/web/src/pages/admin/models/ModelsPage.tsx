import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Database } from "lucide-react"
import { AdminLayout } from "../../../components/layout/AdminLayout"
import { PageHeader } from "../../../components/layout/PageHeader"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { EmptyState } from "../../../components/ui/empty-state"
import { DetailRail } from "../../../components/ui/detail-rail"
import { Ledger, LedgerGroup, LedgerHeader, LedgerRow, col } from "../../../components/ui/ledger"
import { useWorkspaceId } from "../../../lib/workspace"
import { useMyWorkspaces } from "../../../lib/api-workspaces"
import { useAdminView } from "../../../lib/admin-router"
import { useModelCatalog, catalogHarnessLabels } from "../../../lib/model-catalog"
import { CatalogDialog, type CatalogEdit } from "./CatalogDialog"
const columns = [col.title(), col.id(160), col.meta(180)]
export function ModelsPage() {
  const workspace = useWorkspaceId()
  return <WorkspaceModels key={workspace} workspace={workspace} />
}
function WorkspaceModels({ workspace }: { workspace: string | null }) {
  const { t } = useTranslation("admin")
  const { navigate, entityId } = useAdminView()
  const workspaces = useMyWorkspaces()
  const role = workspaces.data?.workspaces.find(row => row.id === workspace)?.role
  const canManage = role === "owner" || role === "admin"
  const catalog = useModelCatalog(workspace)
  const [search, setSearch] = useState("")
  const [edit, setEdit] = useState<CatalogEdit | null>(null)
  const selected = catalog.data?.providers.find(row => row.id === entityId)
  const models = catalog.data?.models ?? []
  const query = search.trim().toLowerCase()
  const groups = (catalog.data?.providers ?? []).map(provider => {
    const providerMatches = `${provider.name} ${provider.base_url}`.toLowerCase().includes(query)
    const children = models.filter(model => model.provider_id === provider.id && (providerMatches || `${model.name} ${model.model_key}`.toLowerCase().includes(query)))
    return { provider, children, visible: providerMatches || children.length > 0 }
  }).filter(group => group.visible)
  return <AdminLayout activeMenu="models" fullBleed><div className="flex min-h-0 flex-1">
    <div className="flex min-w-0 flex-1 flex-col">
      <PageHeader className="static mx-0 mb-0" title={t("catalog.title")} action={canManage ? <Button disabled={!workspace} onClick={() => setEdit({ kind: "provider" })}>{t("catalog.addProvider")}</Button> : undefined} />
      <div className="border-b border-line px-5 py-3"><Input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder={t("catalog.search")} aria-label={t("catalog.search")} className="max-w-sm" /></div>
      <div className="min-h-0 flex-1 overflow-auto">
        {!workspace ? <p className="p-5">{t("core.selectWorkspace")}</p> : catalog.isLoading ? <p role="status" className="p-5">{t("core.loading")}</p> : catalog.error ? <div role="alert" className="p-5"><p>{catalog.error.message}</p><Button variant="outline" onClick={() => void catalog.refetch()}>{t("core.retry")}</Button></div> : groups.length === 0 ? <EmptyState icon={Database} title={t(search ? "core.noResults" : "catalog.empty")} description={t("catalog.providerHint")} /> : <Ledger columns={columns} role="region" aria-label={t("catalog.title")}>
          <LedgerHeader><span>{t("catalog.model")}</span><span>{t("catalog.modelKey")}</span><span>Harness</span></LedgerHeader>
          {groups.map(({ provider, children }) => <LedgerGroup key={`${provider.id}:${query}`} label={provider.name} count={children.length} selected={provider.id === entityId} onSelect={() => navigate("models", { id: provider.id })}>
            {children.map(model => <LedgerRow key={model.id} interactive={false} className="h-8 text-xs">
              <span className="truncate pl-4 text-fg-muted">{model.name}</span><span className="truncate text-fg-muted">{model.model_key}</span><span className="truncate text-fg-muted">{catalogHarnessLabels(model)}</span>
            </LedgerRow>)}
            {children.length === 0 && <li className="border-b border-line px-6 py-3 text-xs text-fg-muted">{t("catalog.noModels")}</li>}
          </LedgerGroup>)}
        </Ledger>}
      </div>
    </div>
    {selected && <DetailRail header={<strong className="truncate">{selected.name}</strong>} onClose={() => navigate("models")}>
      <div className="space-y-5">
        <div><p className="break-all text-sm text-fg-muted">{selected.base_url}</p><p className="mt-2 text-xs">{selected.protocol} · {t("catalog.configured")}</p></div>
        {canManage && <div className="flex gap-2"><Button variant="outline" size="sm" onClick={() => setEdit({ kind: "provider", provider: selected })}>{t("catalog.editProvider")}</Button><Button variant="ghost" size="sm" onClick={() => setEdit({ kind: "delete", resource: "model-providers", id: selected.id, name: selected.name })}>{t("core.delete")}</Button></div>}
        <div className="flex items-center justify-between"><h2 className="text-sm font-medium">{t("catalog.title")}</h2>{canManage && <Button variant="outline" size="sm" onClick={() => setEdit({ kind: "model", provider: selected })}>{t("catalog.addModel")}</Button>}</div>
        {models.filter(model => model.provider_id === selected.id).map(model => <div key={model.id} className="border-t border-line py-3"><p className="text-sm font-medium">{model.name}</p><p className="break-all font-mono text-xs text-fg-muted">{model.model_key}</p>{canManage && <div className="mt-2 flex gap-2"><Button variant="ghost" size="sm" onClick={() => setEdit({ kind: "model", provider: selected, model })}>{t("core.edit")}</Button><Button variant="ghost" size="sm" onClick={() => setEdit({ kind: "delete", resource: "models", id: model.id, name: model.name })}>{t("core.delete")}</Button></div>}</div>)}
        {!models.some(model => model.provider_id === selected.id) && <p className="text-sm text-fg-muted">{t("catalog.noModels")}</p>}
      </div>
    </DetailRail>}
    {edit && workspace && canManage && <CatalogDialog workspace={workspace} edit={edit} onClose={() => setEdit(null)} />}
  </div></AdminLayout>
}
