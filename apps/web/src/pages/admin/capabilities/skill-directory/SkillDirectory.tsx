import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Check, PackageCheck, Sparkles } from "lucide-react"

import { Button } from "../../../../components/ui/button"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { Skeleton } from "../../../../components/ui/skeleton"
import {
  marketplaceSourceName,
  type MarketplaceCapability,
  useImportSkillDirectoryItem,
  useMarketplaceList,
  useSkillDirectory,
  useSkillDirectoryDetail,
  type SkillDirectoryItem,
} from "../../../../lib/api-marketplace"
import { useWorkspaceId } from "../../../../lib/workspace"
import { DirectorySkillCard, PublishedSkillCard } from "./SkillDirectoryCard"
import { ImportSkillDirectoryDialog } from "./ImportSkillDirectoryDialog"
import { SkillDirectoryDetail } from "./SkillDirectoryDetail"

type DirectorySort = "featured" | "name" | "newest"

interface SkillDirectoryProps {
  itemID: string | null
  query: string
  canImport: boolean
  onSelectItem: (id: string | null) => void
  onSelectMarketplaceItem: (id: string | null) => void
  onInstallMarketplace: (capability: MarketplaceCapability) => void
  canManageMarketplace: boolean
  onDeleteMarketplace: (capability: MarketplaceCapability) => void
  onViewCapability: (capabilityID: string) => void
}

export function SkillDirectory({
  itemID,
  query,
  canImport,
  onSelectItem,
  onSelectMarketplaceItem,
  onInstallMarketplace,
  canManageMarketplace,
  onDeleteMarketplace,
  onViewCapability,
}: SkillDirectoryProps) {
  const { t } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const directoryQ = useSkillDirectory(workspaceID)
  const marketplaceQ = useMarketplaceList(itemID ? null : workspaceID)
  const importMut = useImportSkillDirectoryItem(workspaceID)
  const [category, setCategory] = useState("")
  const [verifiedOnly, setVerifiedOnly] = useState(false)
  const [sort, setSort] = useState<DirectorySort>("featured")
  const [confirmID, setConfirmID] = useState<string | null>(null)
  const [success, setSuccess] = useState<{ name: string; capabilityID: string } | null>(null)

  const detailID = confirmID ?? itemID
  const detailQ = useSkillDirectoryDetail(workspaceID, detailID)
  const items = useMemo(() => directoryQ.data?.items ?? [], [directoryQ.data?.items])
  const categories = useMemo(
    () => Array.from(new Set(items.flatMap((item) => item.categories))).sort((left, right) => left.localeCompare(right)),
    [items],
  )
  const filtered = useMemo(() => filterItems(items, query, category, verifiedOnly, sort), [items, query, category, verifiedOnly, sort])
  const publishedSkills = useMemo(() => {
    if (category || verifiedOnly) return []
    const installedIDs = new Set(items.flatMap((item) => item.installed_capability_id ? [item.installed_capability_id] : []))
    const directoryNames = new Set(items.map((item) => normalizeSkillName(item.name)))
    const needle = query.trim().toLocaleLowerCase()
    return (marketplaceQ.data ?? [])
      .filter((item) => {
        if (item.type !== "skill" || installedIDs.has(item.id)) return false
        // Older self-published skills may not have catalog metadata, so their
        // capability ID cannot be used to deduplicate them with a catalog item.
        // Keep skills from other workspaces visible even when their names match.
        if ((item.self_published || item.source_workspace_id === workspaceID) && directoryNames.has(normalizeSkillName(item.name))) return false
        if (!needle) return true
        return [item.name, item.description ?? "", marketplaceSourceName(item)].join(" ").toLocaleLowerCase().includes(needle)
      })
      .sort((left, right) => left.name.localeCompare(right.name))
  }, [category, items, marketplaceQ.data, query, verifiedOnly, workspaceID])
  const cards = useMemo(() => [
    ...filtered.map((item) => ({ kind: "directory" as const, item })),
    ...publishedSkills.map((item) => ({ kind: "marketplace" as const, item })),
  ], [filtered, publishedSkills])
  const selectedSummary = items.find((item) => item.id === itemID) ?? null
  const selected = detailQ.data?.id === itemID ? detailQ.data : selectedSummary
  const confirmItem = detailQ.data?.id === confirmID ? detailQ.data : items.find((item) => item.id === confirmID) ?? null

  const requestImport = (id: string) => {
    if (!canImport) return
    importMut.reset()
    setConfirmID(id)
  }
  const closeImportDialog = () => {
    importMut.reset()
    setConfirmID(null)
  }
  const confirmImport = () => {
    if (!confirmID || !confirmItem || confirmItem.installed) return
    importMut.mutate(confirmID, {
      onSuccess: (result) => {
        setSuccess({ name: confirmItem.name, capabilityID: result.capability_id })
        closeImportDialog()
      },
    })
  }

  const importDialog = (
    <ImportSkillDirectoryDialog
      open={confirmID !== null}
      item={confirmItem}
      loading={detailQ.isLoading}
      error={detailQ.error}
      pending={importMut.isPending}
      mutationError={importMut.error}
      onRetry={() => void detailQ.refetch()}
      onOpenChange={(open) => !open && closeImportDialog()}
      onConfirm={confirmImport}
    />
  )

  if (itemID) {
    return (
      <>
        {success ? <SuccessBanner success={success} onViewCapability={onViewCapability} /> : null}
        <SkillDirectoryDetail
          item={selected}
          loading={detailQ.isLoading}
          error={detailQ.error}
          canImport={canImport}
          onBack={() => onSelectItem(null)}
          onRetry={() => void detailQ.refetch()}
          onImport={() => requestImport(itemID)}
          onViewCapability={onViewCapability}
        />
        {importDialog}
      </>
    )
  }

  return (
    <div className="space-y-4" data-testid="skill-directory">
      <div className="rounded-xl border border-line bg-surface px-5 py-4">
        <div className="flex items-start gap-3">
          <div>
            <div className="flex items-center gap-2"><Sparkles className="h-4 w-4 text-fg-subtle" /><h2 className="text-lg font-semibold text-fg">{t("capabilities.skillDirectory.title")}</h2></div>
            <p className="mt-1 max-w-2xl text-sm leading-5 text-fg-muted">{t("capabilities.skillDirectory.description")}</p>
          </div>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <div className="flex min-w-0 flex-1 flex-wrap gap-1.5" aria-label={t("capabilities.skillDirectory.filters.category")}>
            <FilterChip active={!category} onClick={() => setCategory("")}>{t("capabilities.skillDirectory.filters.allCategories")}</FilterChip>
            {categories.map((value) => <FilterChip key={value} active={category === value} onClick={() => setCategory(value)}>{value}</FilterChip>)}
          </div>
          <label className="inline-flex h-8 select-none items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-sm text-fg-muted">
            <input type="checkbox" checked={verifiedOnly} onChange={(event) => setVerifiedOnly(event.target.checked)} className="h-3.5 w-3.5 rounded border-line-strong" />
            {t("capabilities.skillDirectory.filters.verified")}
          </label>
          <select aria-label={t("capabilities.skillDirectory.filters.sort")} value={sort} onChange={(event) => setSort(event.target.value as DirectorySort)} className="h-8 rounded-md border border-line bg-surface px-2.5 text-sm text-fg-muted outline-none focus:border-line-strong">
            <option value="featured">{t("capabilities.skillDirectory.sort.featured")}</option>
            <option value="name">{t("capabilities.skillDirectory.sort.name")}</option>
            <option value="newest">{t("capabilities.skillDirectory.sort.newest")}</option>
          </select>
        </div>
      </div>

      {success ? <SuccessBanner success={success} onViewCapability={onViewCapability} /> : null}
      {directoryQ.error ? <ErrorState title={t("capabilities.skillDirectory.loadError.title")} description={t("capabilities.skillDirectory.loadError.description")} onRetry={() => void directoryQ.refetch()} /> : null}
      {marketplaceQ.error ? <ErrorState title={t("capabilities.marketplace.loadError.title")} description={marketplaceQ.error instanceof Error ? marketplaceQ.error.message : t("capabilities.marketplace.loadError.description")} onRetry={() => void marketplaceQ.refetch()} /> : null}
      {cards.length === 0 && (directoryQ.isLoading || marketplaceQ.isLoading) ? (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="skill-directory-loading">{Array.from({ length: 6 }).map((_, index) => <Skeleton key={index} className="h-52 w-full" />)}</div>
      ) : cards.length === 0 && !directoryQ.error && !marketplaceQ.error ? (
        <EmptyState icon={PackageCheck} title={t("capabilities.skillDirectory.empty.title")} description={t("capabilities.skillDirectory.empty.description")} />
      ) : cards.length > 0 ? (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="skill-marketplace-grid">
          {cards.map((card) => card.kind === "directory" ? (
            <DirectorySkillCard key={`directory:${card.item.id}`} item={card.item} canImport={canImport} onOpen={() => onSelectItem(card.item.id)} onImport={() => requestImport(card.item.id)} onViewCapability={onViewCapability} />
          ) : (
            <PublishedSkillCard key={`marketplace:${card.item.id}`} capability={card.item} canManage={canManageMarketplace} onOpen={() => onSelectMarketplaceItem(card.item.id)} onInstall={() => onInstallMarketplace(card.item)} onDelete={() => onDeleteMarketplace(card.item)} onViewCapability={() => onViewCapability(card.item.id)} />
          ))}
        </div>
      ) : null}
      {importDialog}
    </div>
  )
}

function filterItems(items: SkillDirectoryItem[], query: string, category: string, verifiedOnly: boolean, sort: DirectorySort) {
  const needle = query.trim().toLocaleLowerCase()
  const filtered = items.filter((item) => {
    if (category && !item.categories.includes(category)) return false
    if (verifiedOnly && !item.verified) return false
    if (!needle) return true
    return [item.name, item.description, item.publisher.name, ...item.categories].join(" ").toLocaleLowerCase().includes(needle)
  })
  return filtered.sort((left, right) => {
    if (sort === "name") return left.name.localeCompare(right.name)
    if (sort === "newest") return right.version.localeCompare(left.version) || left.featured_rank - right.featured_rank
    return left.featured_rank - right.featured_rank || left.name.localeCompare(right.name)
  })
}

function normalizeSkillName(name: string): string {
  return name.trim().toLocaleLowerCase().replace(/[\s_-]+/g, " ")
}

function SuccessBanner({ success, onViewCapability }: { success: { name: string; capabilityID: string }; onViewCapability: (capabilityID: string) => void }) {
  const { t } = useTranslation("admin")
  return <div className="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-line bg-surface px-4 py-3" role="status"><span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-muted text-fg"><Check className="h-4 w-4" /></span><p className="min-w-0 flex-1 text-sm text-fg">{t("capabilities.skillDirectory.import.success", { name: success.name })}</p><Button variant="outline" size="sm" onClick={() => onViewCapability(success.capabilityID)}>{t("capabilities.skillDirectory.actions.viewCapability")}</Button></div>
}

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: string }) {
  return <button type="button" aria-pressed={active} onClick={onClick} className={`h-8 rounded-md border px-2.5 text-sm transition-colors ${active ? "border-line-strong bg-surface-muted text-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg"}`}>{children}</button>
}
