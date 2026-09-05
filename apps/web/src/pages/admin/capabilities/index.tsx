// Admin capability page — list + detail.
import { useEffect, useMemo, useState, type KeyboardEvent, type ReactNode } from "react"
import { useQueries } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import {
  ArrowUpRight,
  Check,
  ListFilter,
  Loader2,
  MoreHorizontal,
  PackageCheck,
  Pencil,
  Plus,
  Search,
  Share2,
  Trash2,
  Wrench,
} from "lucide-react"

import { AdminLayout } from "../../../components/layout/AdminLayout"
import { PageHeader } from "../../../components/layout/PageHeader"
import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { RailSection } from "../../../components/ui/detail-rail"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Field } from "../../../components/ui/label"
import { Input } from "../../../components/ui/input"
import { InitialTile, Ledger, LedgerGroup, LedgerHeader, LedgerNum, LedgerRow } from "../../../components/ui/ledger"
import { OffsetPagination } from "../../../components/ui/offset-pagination"
import { PropertyList, Property } from "../../../components/ui/property-list"
import { Skeleton } from "../../../components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "../../../components/ui/tabs"
import { ApiError } from "../../../lib/api-client"
import { noUnreachableRetry } from "../../../lib/api-client"
import { useAgents } from "../../../lib/api-agents"
import {
  KEY_AGENT_CAPABILITIES,
  KEY_CAPABILITY_VERSIONS,
  agentCapabilityVersionID,
  listCapabilityVersions,
  listAgentCapabilities,
  skillVersionRef,
  useCapabilitiesQuery,
  useCapabilityQuery,
  useCapabilityVersionsQuery,
  useUpdateCapability,
} from "../../../lib/api-capabilities"
import {
  useDelete,
  useDeprecate,
  useInstallCount,
  useMarketplaceEnabledAgents,
  useMCPDirectory,
  usePublish,
  useTargetMarketplaceInstalls,
  useUndeprecate,
  useUninstall,
  useUnpublish,
  type MarketplaceCapability,
  type TargetMarketplaceInstall,
  marketplaceSourceName,
} from "../../../lib/api-marketplace"
import { navigateAdmin, useAdminView } from "../../../lib/admin-router"
import type { AgentCapability, Capability, CapabilityVersion } from "../../../lib/api-types"
import { useMyWorkspaces } from "../../../lib/api-workspaces"
import { useWorkspaceId } from "../../../lib/workspace"
import { useRelativeTime } from "../../../lib/relative-time"
import { requiredCredentialsLabel } from "../../../lib/credential-kind-ui"
import { CapabilityTypeBadge } from "./CapabilityTypeBadge"
import { BackLink, InlineNotice } from "./notices"
import { MarketplaceCapabilityDetail } from "./MarketplaceCapabilityDetail"
import { MarketplaceTab } from "./MarketplaceTab"
import { DeprecateCapabilityDialog } from "./DeprecateCapabilityDialog"
import { DeleteCapabilityDialog } from "./DeleteCapabilityDialog"
import { ImportCapabilityDialog } from "./ImportCapabilityDialog"
import { AddCapabilityVersionDialog } from "./AddCapabilityVersionDialog"
import { UninstallMarketplaceDialog } from "./UninstallMarketplaceDialog"
import type { DirectoryFilterState, DirectorySort } from "./mcp-directory/filters"

export { CapabilityTypeBadge } from "./CapabilityTypeBadge"

type MarketAction = "publish" | "unpublish" | "deprecate" | "undeprecate" | null
type MarketCapabilityAction = Exclude<MarketAction, null>

interface AgentInstallation {
  agentID: string
  agentName: string
  version: string
  latest: boolean
}

/** "" = every type; "bundle" is the server's name for plugin bundles. */
type CapabilityTypeFilter = "" | "mcp" | "skill" | "bundle"
type PageTab = "workspace" | "marketplace" | "connectors" | "skills"

const PAGE_SIZE = 20
const PAGE_TABS: PageTab[] = ["workspace", "marketplace", "connectors", "skills"]
const TYPE_FILTERS: { value: CapabilityTypeFilter; label: string }[] = [
  { value: "mcp", label: "MCP" },
  { value: "skill", label: "Skill" },
  { value: "bundle", label: "Plugin" },
]

/** name (+type, +description) · version · source · enabled agents · credentials · updated · actions */
const LEDGER_COLUMNS = "minmax(0,1fr) 88px 132px 96px 150px 80px 64px"

export function CapabilitiesPage() {
  const { t, i18n } = useTranslation("admin")
  const wid = useWorkspaceId()
  const { navigate } = useAdminView()
  const fmtAgo = useRelativeTime()
  const [query, setQuery] = useState("")
  const [typeFilter, setTypeFilter] = useState<CapabilityTypeFilter>("")
  const [hideInstalled, setHideInstalled] = useState(false)
  const [directoryFilters, setDirectoryFilters] = useState<DirectoryFilterState>({ category: "", verifiedOnly: false, sort: "featured" })
  const [page, setPage] = useState(1)
  const debouncedQuery = useDebouncedValue(query, 250)
  // Reset to page 1 whenever the user changes filters.
  useEffect(() => {
    setPage(1)
  }, [debouncedQuery, typeFilter])
  const capsQ = useCapabilitiesQuery(wid, debouncedQuery, typeFilter, page, PAGE_SIZE)
  const agentsQ = useAgents(wid)
  const workspacesQ = useMyWorkspaces()
  const marketplaceInstallsQ = useTargetMarketplaceInstalls(wid)
  const publishMut = usePublish(wid)
  const unpublishMut = useUnpublish(wid)
  const deprecateMut = useDeprecate(wid)
  const undeprecateMut = useUndeprecate(wid)
  const uninstallMut = useUninstall(wid)
  const deleteMut = useDelete(wid)
  const [importOpen, setImportOpen] = useState(false)
  const [addVersionCapability, setAddVersionCapability] = useState<Capability | null>(null)
  const [marketTarget, setMarketTarget] = useState<{ action: MarketCapabilityAction; capability: Capability } | null>(null)
  const [marketClientError, setMarketClientError] = useState<string | null>(null)
  const [uninstallTarget, setUninstallTarget] = useState<TargetMarketplaceInstall | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Capability | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const workspaceRole = workspacesQ.data?.workspaces.find((w) => w.id === wid)?.role
  const isAdmin = workspaceRole === "owner" || workspaceRole === "admin"
  const canImportDirectory = isAdmin || workspaceRole === "member"
  const marketInstallCountQ = useInstallCount(wid, marketTarget?.capability.id ?? null)
  const uninstallAgentsQ = useMarketplaceEnabledAgents(wid, uninstallTarget?.id ?? null)

  const agents = useMemo(() => agentsQ.data?.agents ?? [], [agentsQ.data])
  const agentCapabilityQueries = useQueries({
    queries: agents.map((agent) => ({
      queryKey: KEY_AGENT_CAPABILITIES(wid ?? "_none", agent.id),
      queryFn: () => listAgentCapabilities(wid, agent.id),
      enabled: !!wid,
      retry: noUnreachableRetry,
      staleTime: 30_000,
    })),
  })

  const routeTab = useAdminView().tab
  const itemParam = useUrlParam("item")
  // Tab is URL-driven; default lands on the workspace list. A selected
  // marketplace / directory item also lives in the URL (`item`).
  const pageTab: PageTab = itemParam?.startsWith("mcp:")
    ? "connectors"
    : routeTab === "marketplace" || routeTab === "connectors" || routeTab === "skills"
      ? routeTab
      : itemParam
        ? "marketplace"
        : "workspace"
  const marketplaceTypeFilter: "" | "mcp" | "skill" = typeFilter === "bundle" ? "" : typeFilter
  const setPageTab = (next: PageTab) => {
    if (next !== "workspace" && typeFilter === "bundle") setTypeFilter("")
    navigate("capabilities", { tab: next === "workspace" ? null : next, item: null })
  }
  const selectedItem = pageTab === "marketplace" || pageTab === "connectors" ? itemParam : null
  // Directory categories feed the filter menu; React Query dedupes this
  // against the directory list itself.
  const directoryQ = useMCPDirectory(pageTab === "connectors" ? wid : null)
  const directoryCategories = useMemo(
    () => Array.from(new Set((directoryQ.data?.items ?? []).flatMap((item) => item.categories))).sort((a, b) => a.localeCompare(b)),
    [directoryQ.data?.items],
  )
  const goToAgentsForCapability = (capability: MarketplaceCapability) => {
    const url = new URL(window.location.href)
    url.searchParams.set("admin", "agents")
    url.searchParams.delete("id")
    url.searchParams.delete("tab")
    url.searchParams.delete("item")
    url.searchParams.set("pendingCapability", capability.id)
    window.history.pushState({}, "", url.toString())
    window.dispatchEvent(new Event("admin:navigate"))
  }
  const ownCapabilities = useMemo(() => capsQ.data?.capabilities ?? [], [capsQ.data?.capabilities])
  // In paginated mode the server returns the page-sliced marketplace installs
  // alongside the page-sliced own capabilities, so we use those instead of
  // the separate full-list endpoint. The standalone endpoint stays mounted to
  // compute totals (and as a fallback if paginated mode is off).
  const pageInstalls = useMemo(
    () => (capsQ.data?.marketplace_installs ?? []) as TargetMarketplaceInstall[],
    [capsQ.data?.marketplace_installs],
  )
  const allInstalls = marketplaceInstallsQ.data ?? []
  const usingServerPage = capsQ.data?.total !== undefined
  const visibleTotal = usingServerPage
    ? capsQ.data?.total ?? 0
    : ownCapabilities.length + allInstalls.length
  const filtersActive = !!debouncedQuery.trim() || typeFilter !== ""
  const versionSummary = useCapabilityVersionSummary(wid, ownCapabilities)
  const latestVersions = versionSummary.latest
  const selectedLatestVersion = addVersionCapability ? latestVersions.get(addVersionCapability.id) : undefined
  const uninstallAgents = uninstallAgentsQ.data ?? uninstallTarget?.enabled_agents ?? []
  const enabledCounts = useMemo(
    () => countCapabilityInstalls(agentCapabilityQueries.map((q) => q.data?.installed ?? [])),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [agentCapabilityQueries.map((q) => q.dataUpdatedAt).join(":")],
  )

  const err = capsQ.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable
  const marketPendingID = marketTarget && (publishMut.isPending || unpublishMut.isPending || deprecateMut.isPending || undeprecateMut.isPending)
    ? marketTarget.capability.id
    : null
  const uninstallPendingID = uninstallTarget && uninstallMut.isPending ? uninstallTarget.id : null
  const deletePendingID = deleteTarget && deleteMut.isPending ? deleteTarget.id : null

  const requestMarketAction = (action: MarketCapabilityAction, capability: Capability) => {
    setMarketClientError(null)
    if (action === "publish" && capability.type === "mcp") {
      const leakingVersion = (versionSummary.byCapability.get(capability.id) ?? []).find((version) => containsPlaintextSecretPattern(JSON.stringify(version.content ?? {})))
      if (leakingVersion) {
        setMarketClientError(t("capabilities.errors.plaintextSecretPattern", { version: leakingVersion.version }))
        return
      }
    }
    setMarketTarget({ action, capability })
  }

  const submitMarketAction = () => {
    if (!marketTarget) return
    const { action, capability } = marketTarget
    const mutation = action === "publish"
      ? publishMut
      : action === "unpublish"
        ? unpublishMut
        : action === "deprecate"
          ? deprecateMut
          : undeprecateMut
    mutation.mutate(capability.id, {
      onSuccess: () => {
        setToast(t(`capabilities.marketStatus.toast.${action}`, { name: capability.name }))
        setMarketTarget(null)
      },
    })
  }

  const pageTitle = t("capabilities.page.title")
  const openCapability = (cap: Capability, fromMarketplace: boolean) =>
    navigate("capabilities", { id: cap.id, from: fromMarketplace ? "marketplace" : null })

  const own = ownCapabilities
  const installs = pageInstalls
  const renderRow = (cap: Capability, fromMarketplace: boolean) => {
    const marketCap = cap as TargetMarketplaceInstall
    const enabledCount = fromMarketplace
      ? marketCap.enabled_agent_count ?? enabledCounts.get(cap.id) ?? 0
      : enabledCounts.get(cap.id) ?? 0
    const version = fromMarketplace
      ? marketCap.pinned_version ?? marketCap.latest_version ?? marketCap.latest_published_version
      : latestVersions.get(cap.id)?.version
    return (
      <CapabilityRow
        key={`${fromMarketplace ? "market" : "own"}-${cap.id}`}
        capability={cap}
        version={version}
        source={fromMarketplace ? marketplaceSourceName(marketCap) : t("capabilities.tabs.workspace")}
        deprecatedLabel={fromMarketplace ? t("capabilities.deprecated.badgeTarget") : t("capabilities.deprecated.badgeSource")}
        enabledCount={enabledCount}
        credentials={requiredCredentialsLabel(cap.required_credentials, i18n.language, t("capabilities.credentials.none"))}
        age={fmtAgo(cap.updated_at ?? cap.created_at)}
        onOpen={() => openCapability(cap, fromMarketplace)}
        actions={
          <CapabilityRowActions
            capability={cap}
            fromMarketplace={fromMarketplace}
            isAdmin={isAdmin}
            marketPending={marketPendingID === cap.id}
            uninstallPending={uninstallPendingID === cap.id}
            deletePending={deletePendingID === cap.id}
            onAddVersion={() => setAddVersionCapability(cap)}
            onMarketAction={(action) => requestMarketAction(action, cap)}
            onUninstall={() => setUninstallTarget(marketCap)}
            onDelete={() => setDeleteTarget(cap)}
          />
        }
      />
    )
  }

  const workspaceBody = err ? (
    <div className="px-4 pt-4">
      <ErrorState
        title={isUnreachable ? t("capabilities.loadError.unreachable.title") : t("capabilities.loadError.title")}
        description={isUnreachable ? t("capabilities.loadError.unreachable.description") : err instanceof Error ? err.message : t("capabilities.loadError.description")}
        hint={isUnreachable ? t("capabilities.loadError.unreachable.hint") : t("capabilities.loadError.hint")}
        onRetry={() => void capsQ.refetch()}
      />
    </div>
  ) : capsQ.isLoading ? (
    <LedgerSkeleton />
  ) : !filtersActive && visibleTotal === 0 ? (
    <EmptyState
      icon={PackageCheck}
      title={t("capabilities.empty.title")}
      description={isAdmin ? undefined : t("capabilities.empty.descriptionMember")}
    />
  ) : own.length + installs.length === 0 ? (
    <EmptyState
      icon={Search}
      title={t("capabilities.emptyFiltered.title")}
      description={t("capabilities.emptyFiltered.description")}
      action={
        <Button size="sm" variant="outline" onClick={() => { setQuery(""); setTypeFilter("") }}>
          {t("capabilities.emptyFiltered.reset")}
        </Button>
      }
    />
  ) : (
    <>
      <Ledger columns={LEDGER_COLUMNS} role="listbox" aria-label={pageTitle}>
        <LedgerHeader>
          <span>{t("capabilities.table.name")}</span>
          <span>{t("capabilities.table.latestVersion")}</span>
          <span>{t("capabilities.marketplaceDetail.source.title")}</span>
          <span className="text-right">{t("capabilities.table.enabledAgents")}</span>
          <span>{t("capabilities.table.credentials")}</span>
          <span className="text-right">{t("capabilities.table.updated")}</span>
          <span />
        </LedgerHeader>
        {installs.length > 0 ? (
          <>
            {own.length > 0 && (
              <LedgerGroup label={t("capabilities.tabs.workspace")} count={own.length}>
                {own.map((cap) => renderRow(cap, false))}
              </LedgerGroup>
            )}
            <LedgerGroup label={t("capabilities.tabs.marketplace")} count={installs.length}>
              {installs.map((cap) => renderRow(cap, true))}
            </LedgerGroup>
          </>
        ) : (
          <ul className="m-0 list-none p-0">{own.map((cap) => renderRow(cap, false))}</ul>
        )}
      </Ledger>
      {usingServerPage && (
        <OffsetPagination
          offset={(page - 1) * PAGE_SIZE}
          limit={PAGE_SIZE}
          total={capsQ.data?.total ?? 0}
          onPrevious={() => setPage((cur) => Math.max(1, cur - 1))}
          onNext={() => setPage((cur) => cur + 1)}
        />
      )}
    </>
  )

  const marketplaceTab = (
    <MarketplaceTab
      view={pageTab === "workspace" ? "marketplace" : pageTab}
      itemID={selectedItem}
      query={query}
      typeFilter={marketplaceTypeFilter}
      hideInstalled={hideInstalled}
      directoryFilters={directoryFilters}
      canImport={canImportDirectory}
      canManage={isAdmin}
      onSelectItem={(item) => navigate("capabilities", { tab: item?.startsWith("mcp:") ? "connectors" : pageTab === "workspace" ? "marketplace" : pageTab, item })}
      onInstall={goToAgentsForCapability}
      onDelete={setDeleteTarget}
      onViewCapability={(capabilityID) => navigate("capabilities", { id: capabilityID, tab: null, item: null })}
    />
  )

  return (
    <AdminLayout activeMenu="capabilities" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
        {selectedItem ? (
          marketplaceTab
        ) : (
          <>
            <PageHeader
              className="static mx-0 mb-0"
              title={pageTitle}
              subtitleFor="capabilities.page.title"
              action={
                <>
                  <div className="relative w-60">
                    <Search
                      className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
                      strokeWidth={1.5}
                      aria-hidden="true"
                    />
                    <Input
                      type="search"
                      className="pl-7"
                      value={query}
                      onChange={(event) => setQuery(event.target.value)}
                      placeholder={t("capabilities.filters.search")}
                      aria-label={t("capabilities.filters.search")}
                    />
                  </div>
                  {pageTab !== "skills" && (
                    <CapabilitiesFilterMenu
                      tab={pageTab}
                      typeFilter={pageTab === "workspace" ? typeFilter : marketplaceTypeFilter}
                      onTypeFilterChange={setTypeFilter}
                      hideInstalled={hideInstalled}
                      onHideInstalledChange={setHideInstalled}
                      directory={directoryFilters}
                      onDirectoryChange={setDirectoryFilters}
                      categories={directoryCategories}
                    />
                  )}
                  {pageTab === "workspace" && (
                    <Button
                      onClick={() => setImportOpen(true)}
                      disabled={!isAdmin}
                      title={isAdmin ? undefined : t("capabilities.permission.adminOnly")}
                    >
                      <Plus strokeWidth={1.5} aria-hidden="true" />
                      {t("capabilities.actions.create")}
                    </Button>
                  )}
                </>
              }
            />

            <div className="flex h-10 shrink-0 items-center border-b border-line px-4">
              <Tabs value={pageTab} onValueChange={(value) => setPageTab(value as PageTab)}>
                <TabsList>
                  {PAGE_TABS.map((tab) => (
                    <TabsTrigger key={tab} value={tab}>
                      {tab === "connectors" ? t("capabilities.mcpDirectory.title") : t(`capabilities.tabs.${tab}`)}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            </div>

            {toast && <InlineNotice tone="success" className="border-b border-line px-4 py-2">{toast}</InlineNotice>}
            {marketClientError && <InlineNotice tone="error" className="border-b border-line px-4 py-2">{marketClientError}</InlineNotice>}

            {pageTab === "workspace" ? workspaceBody : marketplaceTab}
          </>
        )}
      </div>

      <ImportCapabilityDialog
        workspaceID={wid}
        open={importOpen}
        onOpenChange={setImportOpen}
        onCreated={(capabilityID) => {
          setToast(t("capabilities.toast.created", { name: capabilityID }))
        }}
      />
      {addVersionCapability && (
        <AddCapabilityVersionDialog
          workspaceID={wid}
          open={!!addVersionCapability}
          capability={addVersionCapability}
          latestVersion={selectedLatestVersion}
          onOpenChange={(open) => {
            if (open) return
            setAddVersionCapability(null)
          }}
          onCommitted={() => {
            const name = addVersionCapability.name
            setAddVersionCapability(null)
            setToast(t("capabilities.toast.versionAdded", { name }))
          }}
        />
      )}
      {marketTarget && (
        <DeprecateCapabilityDialog
          action={marketTarget.action}
          capability={marketTarget.capability}
          installCount={marketInstallCountQ.data ?? 0}
          pending={publishMut.isPending || unpublishMut.isPending || deprecateMut.isPending || undeprecateMut.isPending}
          error={publishMut.error ?? unpublishMut.error ?? deprecateMut.error ?? undeprecateMut.error}
          onOpenChange={(open) => {
            if (open) return
            setMarketTarget(null)
            publishMut.reset()
            unpublishMut.reset()
            deprecateMut.reset()
            undeprecateMut.reset()
          }}
          onConfirm={submitMarketAction}
        />
      )}
      <DeleteCapabilityDialog
        capability={deleteTarget}
        pending={deleteMut.isPending}
        error={deleteMut.error}
        onOpenChange={(open) => {
          if (open) return
          setDeleteTarget(null)
          deleteMut.reset()
        }}
        onConfirm={() => {
          if (!deleteTarget) return
          const name = deleteTarget.name
          deleteMut.mutate(deleteTarget.id, {
            onSuccess: () => {
              setDeleteTarget(null)
              setToast(t("capabilities.delete.toast.success", { name }))
            },
          })
        }}
      />
      {uninstallTarget && (
        <UninstallMarketplaceDialog
          capability={uninstallTarget}
          agents={uninstallAgents}
          open={!!uninstallTarget}
          pending={uninstallMut.isPending}
          error={uninstallMut.error}
          onOpenChange={(open) => {
            if (open) return
            setUninstallTarget(null)
            uninstallMut.reset()
          }}
          onConfirm={() => {
            uninstallMut.mutate(uninstallTarget.id, {
              onSuccess: () => {
                setToast(t("capabilities.uninstall.toast", { name: uninstallTarget.name }))
                setUninstallTarget(null)
              },
            })
          }}
        />
      )}
    </AdminLayout>
  )
}

/* ------------------------------------------------------------------ */
/*  List row                                                            */
/* ------------------------------------------------------------------ */

function CapabilityRow({
  capability,
  version,
  source,
  deprecatedLabel,
  enabledCount,
  credentials,
  age,
  onOpen,
  actions,
}: {
  capability: Capability
  version?: string
  source: string
  deprecatedLabel: string
  enabledCount: number
  credentials: string
  age: string
  onOpen: () => void
  actions: ReactNode
}) {
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onOpen()
    }
  }
  return (
    <LedgerRow onClick={onOpen} onKeyDown={onKeyDown}>
      <span className="flex min-w-0 items-center gap-2">
        <span className="shrink-0 truncate font-medium">{capability.name}</span>
        <CapabilityTypeBadge type={capability.type} />
        {capability.deprecated_at && <Badge variant="neutral" dot>{deprecatedLabel}</Badge>}
        {capability.description && (
          <span className="min-w-0 truncate text-xs text-fg-muted">· {capability.description}</span>
        )}
      </span>
      <span className={cnMono(!!version)}>{version ?? "—"}</span>
      <span className="truncate text-xs text-fg-muted">{source}</span>
      <LedgerNum>{enabledCount}</LedgerNum>
      <span className="truncate text-xs text-fg-muted">{credentials}</span>
      <span className="truncate text-right text-xs text-fg-muted">{age}</span>
      <span onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        {actions}
      </span>
    </LedgerRow>
  )
}

function cnMono(present: boolean) {
  return present ? "truncate font-mono text-xs text-fg" : "truncate font-mono text-xs text-fg-muted"
}

function LedgerSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3 w-40" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Header filter menu                                                  */
/* ------------------------------------------------------------------ */

const MENU_CONTENT_CLASS = "app-shadow-floating z-50 min-w-[200px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in"
const MENU_ITEM_CLASS = "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"

function MenuRadio({ value, label }: { value: string; label: string }) {
  return (
    <DropdownMenu.RadioItem value={value} className={MENU_ITEM_CLASS}>
      <span className="flex-1">{label}</span>
      <DropdownMenu.ItemIndicator>
        <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
      </DropdownMenu.ItemIndicator>
    </DropdownMenu.RadioItem>
  )
}

function MenuCheck({ checked, onCheckedChange, label }: { checked: boolean; onCheckedChange: (next: boolean) => void; label: string }) {
  return (
    <DropdownMenu.CheckboxItem checked={checked} onCheckedChange={onCheckedChange} className={MENU_ITEM_CLASS}>
      <span className="flex-1">{label}</span>
      <DropdownMenu.ItemIndicator>
        <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
      </DropdownMenu.ItemIndicator>
    </DropdownMenu.CheckboxItem>
  )
}

function MenuSeparator() {
  return <DropdownMenu.Separator className="my-1 h-px bg-line" />
}

function CapabilitiesFilterMenu({
  tab,
  typeFilter,
  onTypeFilterChange,
  hideInstalled,
  onHideInstalledChange,
  directory,
  onDirectoryChange,
  categories,
}: {
  tab: PageTab
  typeFilter: CapabilityTypeFilter
  onTypeFilterChange: (value: CapabilityTypeFilter) => void
  hideInstalled: boolean
  onHideInstalledChange: (value: boolean) => void
  directory: DirectoryFilterState
  onDirectoryChange: (value: DirectoryFilterState) => void
  categories: string[]
}) {
  const { t } = useTranslation("admin")
  const typeOptions = tab === "workspace" ? TYPE_FILTERS : TYPE_FILTERS.filter((opt) => opt.value !== "bundle")
  const activeType = TYPE_FILTERS.find((opt) => opt.value === typeFilter && typeFilter !== "")
  const summary = tab === "connectors" ? directory.category || null : activeType?.label ?? null
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <Button variant="outline" aria-haspopup="menu">
          <ListFilter strokeWidth={1.5} aria-hidden="true" />
          {t("capabilities.filters.label")}
          {summary && <span className="text-fg-muted">· {summary}</span>}
        </Button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={6} className={MENU_CONTENT_CLASS}>
          {tab === "connectors" ? (
            <>
              <DropdownMenu.RadioGroup value={directory.sort} onValueChange={(v) => onDirectoryChange({ ...directory, sort: v as DirectorySort })}>
                <MenuRadio value="featured" label={t("capabilities.mcpDirectory.sort.featured")} />
                <MenuRadio value="name" label={t("capabilities.mcpDirectory.sort.name")} />
              </DropdownMenu.RadioGroup>
              <MenuSeparator />
              <MenuCheck
                checked={directory.verifiedOnly}
                onCheckedChange={(next) => onDirectoryChange({ ...directory, verifiedOnly: next })}
                label={t("capabilities.mcpDirectory.filters.verified")}
              />
              {categories.length > 0 && (
                <>
                  <MenuSeparator />
                  <DropdownMenu.RadioGroup value={directory.category} onValueChange={(v) => onDirectoryChange({ ...directory, category: v })}>
                    <MenuRadio value="" label={t("capabilities.mcpDirectory.filters.allCategories")} />
                    {categories.map((category) => (
                      <MenuRadio key={category} value={category} label={category} />
                    ))}
                  </DropdownMenu.RadioGroup>
                </>
              )}
            </>
          ) : (
            <>
              <DropdownMenu.RadioGroup value={typeFilter} onValueChange={(v) => onTypeFilterChange(v as CapabilityTypeFilter)}>
                <MenuRadio value="" label={t("capabilities.filters.all")} />
                {typeOptions.map((opt) => (
                  <MenuRadio key={opt.value} value={opt.value} label={opt.label} />
                ))}
              </DropdownMenu.RadioGroup>
              {tab === "marketplace" && (
                <>
                  <MenuSeparator />
                  <MenuCheck checked={hideInstalled} onCheckedChange={onHideInstalledChange} label={t("capabilities.marketplace.filters.hideInstalled")} />
                </>
              )}
            </>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

/**
 * Debounce a fast-changing value so the React Query key behind
 * `useCapabilitiesQuery` doesn't fire per keystroke.
 */
function useDebouncedValue<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const handle = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(handle)
  }, [value, delay])
  return debounced
}

/**
 * Subscribe to a specific URL query param. Repaints when admin-router
 * fires its `admin:navigate` event or the user uses browser back/forward.
 */
function useUrlParam(name: string): string | null {
  const [value, setValue] = useState(() => new URLSearchParams(window.location.search).get(name))
  useEffect(() => {
    const handler = () => setValue(new URLSearchParams(window.location.search).get(name))
    window.addEventListener("popstate", handler)
    window.addEventListener("admin:navigate", handler)
    window.addEventListener("app:navigate", handler)
    return () => {
      window.removeEventListener("popstate", handler)
      window.removeEventListener("admin:navigate", handler)
      window.removeEventListener("app:navigate", handler)
    }
  }, [name])
  return value
}

/* ------------------------------------------------------------------ */
/*  Row actions                                                         */
/* ------------------------------------------------------------------ */

function CapabilityRowActions({
  capability,
  fromMarketplace,
  isAdmin,
  marketPending,
  uninstallPending,
  deletePending,
  onAddVersion,
  onMarketAction,
  onUninstall,
  onDelete,
}: {
  capability: Capability
  fromMarketplace: boolean
  isAdmin: boolean
  marketPending: boolean
  uninstallPending: boolean
  deletePending: boolean
  onAddVersion: () => void
  onMarketAction: (action: MarketCapabilityAction) => void
  onUninstall: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  const published = capability.visibility === "public" || capability.scope === "public"
  const disabledByRole = !isAdmin

  // Marketplace rows only support uninstall; the row itself opens the detail.
  if (fromMarketplace) {
    return (
      <RowActions>
        <ActionIconButton
          icon={Trash2}
          label={t("capabilities.rowActions.uninstall")}
          tone="danger"
          busy={uninstallPending}
          disabled={disabledByRole}
          onClick={onUninstall}
        />
      </RowActions>
    )
  }

  // Edit-as-new-version: the primary action opens AddCapabilityVersionDialog,
  // which carries name/description fields too.
  return (
    <RowActions>
      <ActionIconButton
        icon={Pencil}
        label={t("capabilities.rowActions.edit")}
        disabled={disabledByRole}
        onClick={onAddVersion}
      />
      {!disabledByRole && (
        <CapabilityRowMoreMenu
          published={published}
          menuPending={marketPending || deletePending}
          onMarketAction={onMarketAction}
          onDelete={onDelete}
        />
      )}
    </RowActions>
  )
}

/** "More actions" menu for marketplace publishing and delete. */
function CapabilityRowMoreMenu({
  published,
  menuPending,
  onMarketAction,
  onDelete,
}: {
  published: boolean
  menuPending: boolean
  onMarketAction: (action: MarketCapabilityAction) => void
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <Button variant="ghost" size="icon" aria-label={t("capabilities.rowActions.more")} aria-haspopup="menu">
          {menuPending ? <Loader2 className="animate-spin" strokeWidth={1.5} /> : <MoreHorizontal strokeWidth={1.5} />}
        </Button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={6} className={MENU_CONTENT_CLASS}>
          {/*
            "Delete" releases the capability.name workspace-unique index,
            allowing a same-name capability to be re-imported. The server
            rejects deletes that still have bound agents (409).
            "Deprecate" lives on the detail page's market section.
          */}
          <DropdownMenu.Item className={MENU_ITEM_CLASS} onSelect={() => onMarketAction(published ? "unpublish" : "publish")}>
            <Share2 className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
            <span>{t(published ? "capabilities.rowActions.unpublish" : "capabilities.rowActions.publish")}</span>
          </DropdownMenu.Item>
          <DropdownMenu.Item className={MENU_ITEM_CLASS} onSelect={onDelete}>
            <Trash2 className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
            <span>{t("capabilities.rowActions.delete")}</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

/* ------------------------------------------------------------------ */
/*  Detail page                                                         */
/* ------------------------------------------------------------------ */

/** version · created · agents using it */
const VERSION_COLUMNS = "160px minmax(0,1fr) 96px"
/** agent · version · open */
const AGENT_COLUMNS = "minmax(0,1fr) 120px 14px"

export function CapabilityDetailPage({ id }: { id: string }) {
  const { t, i18n } = useTranslation("admin")
  const wid = useWorkspaceId()
  const capQ = useCapabilityQuery(wid, id)
  const versionsQ = useCapabilityVersionsQuery(wid, id)
  const agentsQ = useAgents(wid)
  const workspacesQ = useMyWorkspaces()
  const updateMut = useUpdateCapability(wid)
  const publishMut = usePublish(wid)
  const unpublishMut = useUnpublish(wid)
  const deprecateMut = useDeprecate(wid)
  const undeprecateMut = useUndeprecate(wid)
  const installCountQ = useInstallCount(wid, id)
  const [editOpen, setEditOpen] = useState(false)
  const [addVersionOpen, setAddVersionOpen] = useState(false)
  const [marketAction, setMarketAction] = useState<MarketAction>(null)
  const [marketClientError, setMarketClientError] = useState<string | null>(null)
  const [viewVersion, setViewVersion] = useState<CapabilityVersion | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const workspaceRole = workspacesQ.data?.workspaces.find((w) => w.id === wid)?.role
  const isAdmin = workspaceRole === "owner" || workspaceRole === "admin"
  const capability = capQ.data ?? null
  const versions = versionsQ.data?.versions ?? []
  const latestVersion = versions[0]
  const installationSummary = useCapabilityEnabledAgents(wid, agentsQ.data?.agents ?? [], capability, versions)
  const enabledCount = installationSummary.installations.length

  const fromMarketplace = new URLSearchParams(window.location.search).get("from") === "marketplace"
  const backLabel = t("capabilities.detail.backToList")

  if (fromMarketplace) {
    return (
      <AdminLayout activeMenu="capabilities" fullBleed>
        <MarketplaceCapabilityDetail id={id} />
      </AdminLayout>
    )
  }

  if (capQ.isLoading) {
    return (
      <AdminLayout activeMenu="capabilities">
        <PageHeader backLink={<BackLink label={backLabel} onClick={() => navigateAdmin("capabilities")} />} title={<Skeleton className="h-4 w-40" />} />
        <div className="space-y-3">
          {Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="h-3 w-full max-w-lg" />)}
        </div>
      </AdminLayout>
    )
  }

  if (capQ.error || !capability) {
    return (
      <AdminLayout activeMenu="capabilities">
        <PageHeader backLink={<BackLink label={backLabel} onClick={() => navigateAdmin("capabilities")} />} title={t("capabilities.detail.notFound.title")} />
        <EmptyState
          icon={Wrench}
          title={t("capabilities.detail.notFound.title")}
          description={capQ.error instanceof Error ? capQ.error.message : t("capabilities.detail.notFound.description")}
        />
      </AdminLayout>
    )
  }

  const submitMarketAction = () => {
    const mutation = marketAction === "publish"
      ? publishMut
      : marketAction === "unpublish"
        ? unpublishMut
        : marketAction === "deprecate"
          ? deprecateMut
          : marketAction === "undeprecate"
            ? undeprecateMut
            : null
    if (!mutation) return
    const action = marketAction
    if (!action) return
    mutation.mutate(capability.id, {
      onSuccess: () => {
        setToast(t(`capabilities.marketStatus.toast.${action}`, { name: capability.name }))
        setMarketAction(null)
      },
    })
  }

  const requestMarketAction = (action: MarketAction) => {
    setMarketClientError(null)
    if (action === "publish" && capability.type === "mcp") {
      const leakingVersion = versions.find((version) => containsPlaintextSecretPattern(JSON.stringify(version.content ?? {})))
      if (leakingVersion) {
        setMarketClientError(t("capabilities.errors.plaintextSecretPattern", { version: leakingVersion.version }))
        return
      }
    }
    setMarketAction(action)
  }

  const published = capability.visibility === "public" || capability.scope === "public"
  const deprecated = !!capability.deprecated_at

  return (
    <AdminLayout activeMenu="capabilities">
      <PageHeader
        backLink={<BackLink label={backLabel} onClick={() => navigateAdmin("capabilities")} />}
        title={
          <span className="inline-flex min-w-0 items-center gap-2">
            <span className="truncate">{capability.name}</span>
            <CapabilityTypeBadge type={capability.type} />
          </span>
        }
        action={
          <>
            <Badge variant="neutral" dot>
              {t(deprecated ? "capabilities.status.deprecated" : "capabilities.status.active")}
            </Badge>
            {isAdmin && <Button variant="outline" onClick={() => setEditOpen(true)}>{t("capabilities.actions.edit")}</Button>}
            {isAdmin && (
              <Button onClick={() => setAddVersionOpen(true)}>
                <Plus strokeWidth={1.5} aria-hidden="true" />
                {t("capabilities.actions.addVersion")}
              </Button>
            )}
          </>
        }
      />

      {toast && <InlineNotice tone="success" className="mb-4">{toast}</InlineNotice>}
      {marketClientError && <InlineNotice tone="error" className="mb-4">{marketClientError}</InlineNotice>}

      {capability.description && <p className="mb-4 max-w-3xl text-sm text-fg">{capability.description}</p>}

      <RailSection title={t("capabilities.detail.basic.title")}>
        <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
          <Property label={t("capabilities.table.type")}><CapabilityTypeBadge type={capability.type} /></Property>
          <Property label={t("capabilities.table.credentials")}>
            {requiredCredentialsLabel(capability.required_credentials, i18n.language, t("capabilities.credentials.none"))}
          </Property>
          <Property label={t("capabilities.detail.basic.createdAt")} mono>{formatDate(capability.created_at)}</Property>
          <Property label={t("capabilities.table.latestVersion")} mono>{latestVersion?.version ?? t("capabilities.none")}</Property>
        </PropertyList>
      </RailSection>

      <RailSection title={t("capabilities.versions.title")} meta={versions.length || undefined} className="mt-6">
        {versionsQ.isLoading ? (
          <div className="space-y-2 pt-2">{Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-3 w-full max-w-lg" />)}</div>
        ) : versions.length === 0 ? (
          <EmptyState icon={PackageCheck} title={t("capabilities.versions.empty.title")} description={t("capabilities.versions.empty.description")} className="py-8" />
        ) : (
          <Ledger columns={VERSION_COLUMNS} role="listbox" aria-label={t("capabilities.versions.title")}>
            <LedgerHeader className="static">
              <span>{t("capabilities.versions.table.version")}</span>
              <span>{t("capabilities.versions.table.createdAt")}</span>
              <span className="text-right">{t("capabilities.versions.table.enabledAgents")}</span>
            </LedgerHeader>
            <ul className="m-0 list-none p-0">
              {versions.map((version, index) => (
                <VersionRow
                  key={version.id}
                  version={version}
                  latestLabel={index === 0 ? t("capabilities.versions.latest") : undefined}
                  count={installationSummary.versionCounts.get(version.id) ?? 0}
                  onOpen={() => setViewVersion(version)}
                />
              ))}
            </ul>
          </Ledger>
        )}
      </RailSection>

      <RailSection title={t("capabilities.detail.enabledAgents.title", { count: enabledCount })} className="mt-6">
        {installationSummary.isLoading ? (
          <Skeleton className="mt-2 h-3 w-full max-w-lg" />
        ) : enabledCount === 0 ? (
          <p className="pt-1 text-sm text-fg-muted">{t("capabilities.detail.enabledAgents.empty")}</p>
        ) : (
          <Ledger columns={AGENT_COLUMNS} role="listbox" aria-label={t("capabilities.detail.enabledAgents.title", { count: enabledCount })}>
            <ul className="m-0 list-none p-0">
              {installationSummary.installations.map((item) => (
                <AgentInstallRow
                  key={item.agentID}
                  name={item.agentName}
                  version={item.version}
                  oldLabel={item.latest ? undefined : t("capabilities.detail.enabledAgents.old")}
                  onOpen={() => navigateAdmin("agents", { id: item.agentID, tab: "capabilities" })}
                />
              ))}
            </ul>
          </Ledger>
        )}
      </RailSection>

      {isAdmin && (
        <RailSection title={t("capabilities.marketStatus.title")} className="mt-6">
          <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
            <Property label={t("capabilities.marketStatus.title")}>
              <Badge variant="neutral" dot>
                {published ? t("capabilities.marketStatus.published") : t("capabilities.marketStatus.unpublished")}
              </Badge>
              {deprecated && <span className="text-xs text-fg-muted">{t("capabilities.deprecated.badgeSource")}</span>}
            </Property>
            <Property label={t("capabilities.marketplace.detail.addedCount")} mono>{installCountQ.data ?? 0}</Property>
          </PropertyList>
          <div className="mt-3 flex flex-wrap gap-2">
            {/*
              The deprecate / undeprecate toggle applies to ALL capabilities:
              it is the single "stop offering this" admin signal. Existing
              agent bindings keep working either way (the title says so).
            */}
            <Button
              variant="outline"
              onClick={() => requestMarketAction(deprecated ? "undeprecate" : "deprecate")}
              title={deprecated ? t("capabilities.marketStatus.undeprecateTooltip") : t("capabilities.marketStatus.deprecateTooltip")}
            >
              {deprecated ? t("capabilities.marketStatus.actions.undeprecate") : t("capabilities.marketStatus.actions.deprecate")}
            </Button>
            <Button variant="outline" onClick={() => requestMarketAction(published ? "unpublish" : "publish")}>
              {published ? t("capabilities.marketStatus.actions.unpublish") : t("capabilities.marketStatus.actions.publish")}
            </Button>
          </div>
        </RailSection>
      )}

      <EditCapabilityDialog
        open={editOpen}
        capability={capability}
        pending={updateMut.isPending}
        error={updateMut.error}
        onOpenChange={(open) => {
          setEditOpen(open)
          if (!open) updateMut.reset()
        }}
        onSubmit={(body) => {
          updateMut.mutate({ capabilityID: capability.id, body }, {
            onSuccess: () => {
              setEditOpen(false)
              setToast(t("capabilities.toast.updated", { name: body.name ?? capability.name }))
            },
          })
        }}
      />
      <AddCapabilityVersionDialog
        workspaceID={wid}
        capability={capability}
        latestVersion={latestVersion}
        open={addVersionOpen}
        onOpenChange={(open) => {
          setAddVersionOpen(open)
        }}
        onCommitted={() => {
          setAddVersionOpen(false)
          setToast(t("capabilities.toast.versionAdded", { name: capability.name }))
        }}
      />
      <DeprecateCapabilityDialog
        action={marketAction}
        capability={capability}
        installCount={installCountQ.data ?? 0}
        pending={publishMut.isPending || unpublishMut.isPending || deprecateMut.isPending || undeprecateMut.isPending}
        error={publishMut.error ?? unpublishMut.error ?? deprecateMut.error ?? undeprecateMut.error}
        onOpenChange={(open) => !open && setMarketAction(null)}
        onConfirm={submitMarketAction}
      />
      <ViewVersionContentDialog version={viewVersion} capability={capability} onOpenChange={(open) => !open && setViewVersion(null)} />
    </AdminLayout>
  )
}

function VersionRow({ version, latestLabel, count, onOpen }: { version: CapabilityVersion; latestLabel?: string; count: number; onOpen: () => void }) {
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onOpen()
    }
  }
  return (
    <LedgerRow onClick={onOpen} onKeyDown={onKeyDown}>
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate font-mono text-xs text-fg">{version.version}</span>
        {latestLabel && <span className="shrink-0 text-xs text-fg-muted">{latestLabel}</span>}
      </span>
      <span className="truncate font-mono text-xs text-fg">{formatDate(version.created_at)}</span>
      <LedgerNum muted={count === 0}>{count}</LedgerNum>
    </LedgerRow>
  )
}

function AgentInstallRow({ name, version, oldLabel, onOpen }: { name: string; version: string; oldLabel?: string; onOpen: () => void }) {
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onOpen()
    }
  }
  return (
    <LedgerRow onClick={onOpen} onKeyDown={onKeyDown}>
      <span className="flex min-w-0 items-center gap-1.5">
        <InitialTile name={name} />
        <span className="truncate font-medium">{name}</span>
      </span>
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate font-mono text-xs text-fg">{version}</span>
        {oldLabel && <span className="shrink-0 text-xs text-fg-muted">{oldLabel}</span>}
      </span>
      <ArrowUpRight className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
    </LedgerRow>
  )
}

/**
 * EditCapabilityDialog — minimal name + description editor. The create
 * path goes through the import flow, so this stays small.
 */
function EditCapabilityDialog({ open, capability, pending, error, onOpenChange, onSubmit }: {
  open: boolean
  capability: Capability
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onSubmit: (body: { name?: string; description?: string }) => void
}) {
  const { t } = useTranslation("admin")
  const [name, setName] = useState(capability.name)
  const [description, setDescription] = useState(capability.description ?? "")

  useEffect(() => {
    if (!open) return
    setName(capability.name)
    setDescription(capability.description ?? "")
  }, [open, capability])

  const errMsg = error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null
  const trimmedName = name.trim()
  const validationError = !trimmedName
    ? t("capabilities.errors.nameRequired")
    : trimmedName.length > 50
      ? t("capabilities.errors.nameTooLong")
      : null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t("capabilities.edit.title")}</DialogTitle>
          <DialogDescription>{t("capabilities.edit.description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <Field label={t("capabilities.fields.name.label")} htmlFor="capability-edit-name" hint={t("capabilities.fields.name.help")}>
            <Input id="capability-edit-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={t("capabilities.fields.name.placeholder")} />
          </Field>
          <Field label={t("capabilities.fields.description.label")} htmlFor="capability-edit-description">
            <Input id="capability-edit-description" value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t("capabilities.fields.description.placeholder")} />
          </Field>
          {(validationError || errMsg) && <InlineNotice tone="error">{validationError ?? errMsg}</InlineNotice>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>{t("capabilities.actions.cancel")}</Button>
          <Button disabled={pending || !!validationError} onClick={() => onSubmit({ name: trimmedName, description: description.trim() })}>
            {pending && <Loader2 className="animate-spin" />}
            {t("capabilities.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ViewVersionContentDialog({ version, capability, onOpenChange }: { version: CapabilityVersion | null; capability: Capability; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation("admin")
  const body = version ? renderViewVersionBody(version, capability, t) : null
  return (
    <Dialog open={!!version} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("capabilities.versions.viewContent.title", { version: version?.version })}</DialogTitle>
          <DialogDescription>{t("capabilities.versions.viewContent.description")}</DialogDescription>
        </DialogHeader>
        {body}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("capabilities.actions.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type Translate = ReturnType<typeof useTranslation<"admin">>["t"]

const CODE_BLOCK_CLASS = "m-0 max-h-[420px] overflow-y-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg"

/**
 * Picks the right body for "view version content":
 *   - mcp + canonical_spec present → pretty-print canonical_spec.mcp
 *   - mcp without canonical_spec  → fall back to legacy `content`
 *   - skill + canonical_spec      → parsed slug/title/description/instruction/trigger
 *   - skill without canonical_spec→ legacy git_repo_url/git_ref/path layout
 */
function renderViewVersionBody(version: CapabilityVersion, capability: Capability, t: Translate): ReactNode {
  const canonicalSpec = version.canonical_spec as
    | {
        mcp?: Record<string, unknown>
        skill?: CanonicalSkillSpecView
        plugin?: CanonicalPluginSpecView
        system_prompt?: { prompt?: string; mode?: string }
      }
    | undefined

  if (capability.type === "system_prompt") {
    const sp = canonicalSpec?.system_prompt
    return (
      <div className="space-y-2">
        <PropertyList>
          <Property label="mode" mono>{sp?.mode ?? "append"}</Property>
        </PropertyList>
        <pre className={CODE_BLOCK_CLASS}>{sp?.prompt ?? t("capabilities.none")}</pre>
      </div>
    )
  }

  if (capability.type === "mcp") {
    return <pre className={CODE_BLOCK_CLASS}>{JSON.stringify(canonicalSpec?.mcp ?? version.content ?? {}, null, 2)}</pre>
  }

  if (capability.type === "plugin") {
    const plugin = canonicalSpec?.plugin
    if (!plugin) return <p className="text-sm text-fg-muted">{t("capabilities.none")}</p>
    return (
      <PropertyList className="grid-cols-[120px_minmax(0,1fr)]">
        {plugin.name && <Property label="name" mono>{plugin.name}</Property>}
        {plugin.version && <Property label="version" mono>{plugin.version}</Property>}
        {plugin.description && <Property label="description">{plugin.description}</Property>}
        {plugin.author && <Property label="author">{plugin.author}</Property>}
        {plugin.upload_source && <Property label="upload_source" mono>{plugin.upload_source}</Property>}
        {plugin.oss_key && <Property label="oss_key" mono>{plugin.oss_key}</Property>}
        {plugin.sha256 && <Property label="sha256" mono>{plugin.sha256}</Property>}
        {plugin.github_repo && <Property label="github_repo" mono>{plugin.github_repo}</Property>}
      </PropertyList>
    )
  }

  // skill
  if (canonicalSpec?.skill) {
    const skill = canonicalSpec.skill
    return (
      <div className="space-y-2">
        <PropertyList className="grid-cols-[120px_minmax(0,1fr)]">
          {skill.slug && <Property label="slug" mono>{skill.slug}</Property>}
          {skill.title && <Property label="title">{skill.title}</Property>}
          {skill.description && <Property label="description" className="h-auto min-h-7 whitespace-normal py-1">{skill.description}</Property>}
          {skill.trigger && <Property label="trigger" className="h-auto min-h-7 whitespace-normal py-1">{skill.trigger}</Property>}
        </PropertyList>
        {skill.instruction && <pre className={CODE_BLOCK_CLASS}>{skill.instruction}</pre>}
      </div>
    )
  }
  return (
    <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
      <Property label={t("capabilities.fields.gitRepoUrl.label")} mono>{version.git_repo_url || t("capabilities.none")}</Property>
      <Property label={t("capabilities.fields.gitRef.label")} mono>{skillVersionRef(version) || t("capabilities.none")}</Property>
      <Property label={t("capabilities.fields.path.label")} mono>{version.path || t("capabilities.none")}</Property>
    </PropertyList>
  )
}

interface CanonicalPluginSpecView {
  name?: string
  version?: string
  description?: string
  author?: string
  upload_source?: string
  oss_key?: string
  sha256?: string
  github_repo?: string
  github_ref?: string
  github_path?: string
}

interface CanonicalSkillSpecView {
  slug?: string
  title?: string
  description?: string
  instruction?: string
  trigger?: string
}

function useCapabilityVersionSummary(workspaceID: string | null, capabilities: Capability[]) {
  const queries = useQueries({
    queries: capabilities.map((cap) => ({
      queryKey: KEY_CAPABILITY_VERSIONS(workspaceID ?? "_none", cap.id),
      queryFn: () => listCapabilityVersions(workspaceID as string, cap.id),
      enabled: !!workspaceID,
      retry: noUnreachableRetry,
      staleTime: 30_000,
    })),
  })
  return useMemo(() => {
    const latest = new Map<string, CapabilityVersion>()
    const byCapability = new Map<string, CapabilityVersion[]>()
    queries.forEach((q, idx) => {
      const capabilityID = capabilities[idx].id
      const versions = q.data?.versions ?? []
      byCapability.set(capabilityID, versions)
      if (versions[0]) latest.set(capabilityID, versions[0])
    })
    return { latest, byCapability }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [capabilities, queries.map((q) => q.dataUpdatedAt).join(":")])
}

function useCapabilityEnabledAgents(wid: string | null, agents: Array<{ id: string; name: string }>, capability: Capability | null, versions: CapabilityVersion[]) {
  const queries = useQueries({
    queries: agents.map((agent) => ({
      queryKey: KEY_AGENT_CAPABILITIES(wid ?? "_none", agent.id),
      queryFn: () => listAgentCapabilities(wid, agent.id),
      enabled: !!wid && !!capability,
      retry: noUnreachableRetry,
      staleTime: 30_000,
    })),
  })
  return useMemo(() => {
    const latest = versions[0]
    const versionByID = new Map(versions.map((v) => [v.id, v.version]))
    const versionCounts = new Map<string, number>()
    const installations: AgentInstallation[] = []
    if (!capability) return { installations, versionCounts, isLoading: queries.some((q) => q.isLoading) }
    queries.forEach((q, idx) => {
      for (const item of q.data?.installed ?? []) {
        if (!item.enabled || item.capability_id !== capability.id) continue
        const versionID = agentCapabilityVersionID(item)
        versionCounts.set(versionID, (versionCounts.get(versionID) ?? 0) + 1)
        installations.push({ agentID: agents[idx].id, agentName: agents[idx].name, version: versionByID.get(versionID) ?? "—", latest: versionID === latest?.id })
      }
    })
    return { installations, versionCounts, isLoading: queries.some((q) => q.isLoading) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [capability, agents, versions, queries.map((q) => q.dataUpdatedAt).join(":")])
}

function countCapabilityInstalls(groups: AgentCapability[][]) {
  const counts = new Map<string, number>()
  for (const group of groups) {
    for (const item of group) {
      if (item.enabled) counts.set(item.capability_id, (counts.get(item.capability_id) ?? 0) + 1)
    }
  }
  return counts
}

const plaintextSecretPatternRes = [
  /github_pat_[A-Za-z0-9_]{20,}|ghp_[A-Za-z0-9]{20,}/i,
  /xoxb-[A-Za-z0-9-]{20,}/,
  /AKIA[A-Z0-9]{16}/,
  /eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/,
  /postgres(?:ql)?:\/\/[^\s:@/]+:[^\s:@/]+@/i,
  /(api[_-]?key|access[_-]?token|secret)["'\s:=]+[A-Za-z0-9_./+=-]{32,}/i,
]

function containsPlaintextSecretPattern(value: string) {
  return plaintextSecretPatternRes.some((pattern) => pattern.test(value))
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString() : "—"
}
