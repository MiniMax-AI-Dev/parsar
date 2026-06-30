// Admin capability page — list + detail.
import { useEffect, useMemo, useState } from "react"
import { useQueries } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { ArrowLeft, ArrowUpRight, Eye, Loader2, MoreHorizontal, PackageCheck, Pencil, Plus, Search, Share2, Trash2, Wrench } from "lucide-react"
import * as Tooltip from "@radix-ui/react-tooltip"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"

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
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
import { Skeleton } from "../../../components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../../components/ui/table"
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
import { requiredCredentialsLabel } from "../../../lib/credential-kind-ui"
import { MarketplaceCapabilityDetail } from "./MarketplaceCapabilityDetail"
import { MarketplaceTab } from "./MarketplaceTab"
import { DeprecateCapabilityDialog } from "./DeprecateCapabilityDialog"
import { DeleteCapabilityDialog } from "./DeleteCapabilityDialog"
import { ImportCapabilityDialog } from "./ImportCapabilityDialog"
import { AddCapabilityVersionDialog } from "./AddCapabilityVersionDialog"
import { UninstallMarketplaceDialog } from "./UninstallMarketplaceDialog"

type MarketAction = "publish" | "unpublish" | "deprecate" | "undeprecate" | null
type MarketCapabilityAction = Exclude<MarketAction, null>

interface AgentInstallation {
  agentID: string
  agentName: string
  version: string
  latest: boolean
}

type CapabilityTypeFilter = "all" | "mcp" | "skill" | "plugin" | "system_prompt"
type PageTab = "workspace" | "marketplace"

export function CapabilitiesPage() {
  const { t, i18n } = useTranslation("admin")
  const wid = useWorkspaceId()
  const { navigate } = useAdminView()
  const [query, setQuery] = useState("")
  const [typeFilter, setTypeFilter] = useState<CapabilityTypeFilter>("all")
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const debouncedQuery = useDebouncedValue(query, 250)
  const typeParam = typeFilter === "all" ? "" : typeFilter
  // Reset to page 1 whenever the user changes filters / page size.
  useEffect(() => {
    setPage(1)
  }, [debouncedQuery, typeParam, pageSize])
  const capsQ = useCapabilitiesQuery(wid, debouncedQuery, typeParam, page, pageSize)
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
  // Tab is URL-driven; default lands on workspace. Marketplace tab also
  // owns the selected-detail state via the `item` URL param.
  const pageTab: PageTab = routeTab === "marketplace" || itemParam ? "marketplace" : "workspace"
  const setPageTab = (next: PageTab) => {
    navigate("capabilities", { tab: next === "marketplace" ? "marketplace" : null, item: null })
  }
  const marketplaceItem = pageTab === "marketplace" ? itemParam : null
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
  const ownCapabilities = capsQ.data?.capabilities ?? []
  // In paginated mode the server returns the page-sliced marketplace installs
  // alongside the page-sliced own capabilities, so we use those instead of
  // the separate full-list endpoint. The standalone endpoint stays mounted to
  // compute totals (and as a fallback if paginated mode is off).
  const pageInstalls = (capsQ.data?.marketplace_installs ?? []) as TargetMarketplaceInstall[]
  const allInstalls = marketplaceInstallsQ.data ?? []
  const usingServerPage = capsQ.data?.total !== undefined
  const allCapabilities = useMemo(
    () => [...ownCapabilities, ...pageInstalls],
    [ownCapabilities, pageInstalls],
  )
  // visibleTotal: total across own + marketplace, used to decide whether to
  // show the "workspace empty" state. Server pagination gives us the merged
  // total directly; legacy mode adds the two list lengths.
  const visibleTotal = usingServerPage
    ? capsQ.data?.total ?? 0
    : ownCapabilities.length + allInstalls.length
  const filtersActive = !!debouncedQuery.trim() || typeFilter !== "all"
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

  return (
    <AdminLayout activeMenu="capabilities">
      <PageHeader
        title={t("capabilities.page.title")}
        action={
          isAdmin ? (
            <Button size="sm" onClick={() => setImportOpen(true)}>
              <Plus className="h-3.5 w-3.5" /> {t("capabilities.actions.create")}
            </Button>
          ) : (
            <Tooltip.Provider delayDuration={150}>
              <Tooltip.Root>
                <Tooltip.Trigger asChild>
                  <span>
                    <Button size="sm" disabled>
                      <Plus className="h-3.5 w-3.5" /> {t("capabilities.actions.create")}
                    </Button>
                  </span>
                </Tooltip.Trigger>
                <Tooltip.Portal>
                  <Tooltip.Content className="z-50 rounded-md border border-line bg-surface px-2 py-1 text-sm text-fg-muted shadow-md">
                    {t("capabilities.permission.adminOnly")}
                    <Tooltip.Arrow className="fill-white" />
                  </Tooltip.Content>
                </Tooltip.Portal>
              </Tooltip.Root>
            </Tooltip.Provider>
          )
        }
      />

      {toast && <ToastBanner message={toast} />}
      {marketClientError && <ErrorBanner message={marketClientError} />}

      <Tabs value={pageTab} onValueChange={(value) => setPageTab(value as PageTab)} className="mb-4">
        <TabsList>
          <TabsTrigger value="workspace">{t("capabilities.tabs.workspace")}</TabsTrigger>
          <TabsTrigger value="marketplace">{t("capabilities.tabs.marketplace")}</TabsTrigger>
        </TabsList>
      </Tabs>

      {pageTab === "marketplace" ? (
        <MarketplaceTab
          itemID={marketplaceItem}
          onSelectItem={(item) => navigate("capabilities", { tab: "marketplace", item })}
          onInstall={goToAgentsForCapability}
        />
      ) : err ? (
        <ErrorState
          title={isUnreachable ? t("capabilities.loadError.unreachable.title") : t("capabilities.loadError.title")}
          description={isUnreachable ? t("capabilities.loadError.unreachable.description") : err instanceof Error ? err.message : t("capabilities.loadError.description")}
          hint={isUnreachable ? t("capabilities.loadError.unreachable.hint") : t("capabilities.loadError.hint")}
          onRetry={() => void capsQ.refetch()}
        />
      ) : !capsQ.isLoading && !filtersActive && visibleTotal === 0 ? (
        <EmptyState
          icon={PackageCheck}
          title={t("capabilities.empty.title")}
          description={isAdmin ? t("capabilities.empty.descriptionAdmin") : t("capabilities.empty.descriptionMember")}
          action={isAdmin ? <Button size="sm" onClick={() => setImportOpen(true)}><Plus className="h-3.5 w-3.5" /> {t("capabilities.actions.create")}</Button> : undefined}
        />
      ) : (
        <Tooltip.Provider delayDuration={150}>
          <div className="space-y-3">
            <CapabilitiesFilterBar
              query={query}
              onQueryChange={setQuery}
              typeFilter={typeFilter}
              onTypeFilterChange={setTypeFilter}
            />

            {capsQ.isLoading ? (
              <CapabilitiesLoading />
            ) : allCapabilities.length === 0 ? (
              <EmptyState
                icon={Search}
                title={t("capabilities.emptyFiltered.title")}
                description={t("capabilities.emptyFiltered.description")}
                action={
                  filtersActive ? (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => {
                        setQuery("")
                        setTypeFilter("all")
                      }}
                    >
                      {t("capabilities.emptyFiltered.reset")}
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <>
                <div className="overflow-hidden rounded-lg border border-line bg-surface">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t("capabilities.table.name")}</TableHead>
                        <TableHead>{t("capabilities.table.type")}</TableHead>
                        <TableHead>{t("capabilities.table.latestVersion")}</TableHead>
                        <TableHead>{t("capabilities.table.enabledAgents")}</TableHead>
                        <TableHead>{t("capabilities.table.credentials")}</TableHead>
                        <TableHead className="w-[220px] text-right pr-4">{t("capabilities.table.actions")}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {allCapabilities.map((cap) => {
                        const fromMarketplace = !!cap.from_marketplace || cap.workspace_id !== wid
                        const marketCap = cap as TargetMarketplaceInstall
                        const enabledCount = fromMarketplace ? marketCap.enabled_agent_count ?? enabledCounts.get(cap.id) ?? 0 : enabledCounts.get(cap.id) ?? 0
                        const sourceLine = fromMarketplace
                          ? t("capabilities.marketplace.sourceLine", {
                              source: marketplaceSourceName(marketCap),
                              version: marketCap.pinned_version ?? marketCap.latest_version ?? marketCap.latest_published_version ?? "—",
                            })
                          : ""
                        return (
                          <TableRow key={`${fromMarketplace ? "market" : "own"}-${cap.id}`}>
                            <TableCell className="max-w-[420px]">
                              <button
                                type="button"
                                onClick={() => navigate("capabilities", { id: cap.id, from: fromMarketplace ? "marketplace" : null })}
                                className="flex w-full flex-col items-start text-left transition-colors hover:text-fg"
                              >
                                <span className="flex flex-wrap items-center gap-2 text-base font-medium text-fg hover:underline">
                                  {cap.name}
                                  {cap.deprecated_at && <Badge variant="destructive">{fromMarketplace ? t("capabilities.deprecated.badgeTarget") : t("capabilities.deprecated.badgeSource")}</Badge>}
                                </span>
                                {fromMarketplace && (
                                  <span className="truncate text-xs text-fg-faint">{sourceLine}</span>
                                )}
                                {cap.description && (
                                  <Tooltip.Root>
                                    <Tooltip.Trigger asChild>
                                      <span className="block w-full truncate text-sm text-fg-subtle">{cap.description}</span>
                                    </Tooltip.Trigger>
                                    <Tooltip.Portal>
                                      <Tooltip.Content
                                        className="z-50 max-w-[420px] rounded-md border border-line bg-surface px-2 py-1 text-sm leading-snug text-fg-muted shadow-md"
                                        sideOffset={4}
                                      >
                                        {cap.description}
                                        <Tooltip.Arrow className="fill-white" />
                                      </Tooltip.Content>
                                    </Tooltip.Portal>
                                  </Tooltip.Root>
                                )}
                              </button>
                            </TableCell>
                            <TableCell><CapabilityTypeBadge type={cap.type} /></TableCell>
                            <TableCell className="font-mono text-sm text-fg-muted">{fromMarketplace ? marketCap.pinned_version ?? marketCap.latest_version ?? marketCap.latest_published_version ?? t("capabilities.none") : latestVersions.get(cap.id)?.version ?? t("capabilities.none")}</TableCell>
                            <TableCell className="text-sm text-fg-muted">{enabledCount}</TableCell>
                            <TableCell className="text-sm text-fg-muted">
                              {requiredCredentialsLabel(cap.required_credentials, i18n.language, t("capabilities.credentials.none"))}
                            </TableCell>
                            <TableCell className="pr-4">
                              <CapabilityRowActions
                                capability={cap}
                                fromMarketplace={fromMarketplace}
                                isAdmin={isAdmin}
                                marketPending={marketPendingID === cap.id}
                                uninstallPending={uninstallPendingID === cap.id}
                                deletePending={deletePendingID === cap.id}
                                onView={() => navigate("capabilities", { id: cap.id, from: fromMarketplace ? "marketplace" : null })}
                                onAddVersion={() => setAddVersionCapability(cap)}
                                onMarketAction={(action) => requestMarketAction(action, cap)}
                                onUninstall={() => setUninstallTarget(marketCap)}
                                onDelete={() => setDeleteTarget(cap)}
                              />
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                </div>
                {usingServerPage && (
                  <CapabilitiesPagination
                    page={page}
                    pageSize={pageSize}
                    total={capsQ.data?.total ?? 0}
                    onPageChange={setPage}
                    onPageSizeChange={setPageSize}
                  />
                )}
              </>
            )}
          </div>
        </Tooltip.Provider>
      )}

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


function CapabilitiesFilterBar({
  query,
  onQueryChange,
  typeFilter,
  onTypeFilterChange,
}: {
  query: string
  onQueryChange: (value: string) => void
  typeFilter: CapabilityTypeFilter
  onTypeFilterChange: (value: CapabilityTypeFilter) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Tabs value={typeFilter} onValueChange={(value) => onTypeFilterChange(value as CapabilityTypeFilter)}>
        <TabsList>
          <TabsTrigger value="all">{t("capabilities.filters.all")}</TabsTrigger>
          <TabsTrigger value="mcp">MCP</TabsTrigger>
          <TabsTrigger value="skill">Skill</TabsTrigger>
          <TabsTrigger value="plugin">Plugin</TabsTrigger>
          <TabsTrigger value="system_prompt">{t("capabilities.filters.systemPrompt", "System Prompt")}</TabsTrigger>
        </TabsList>
      </Tabs>
      <div className="relative ml-auto w-full max-w-[280px]">
        <Search className="pointer-events-none absolute left-2.5 top-2.5 h-3.5 w-3.5 text-fg-faint" />
        <Input
          className="pl-8"
          value={query}
          onChange={(event) => onQueryChange(event.target.value)}
          placeholder={t("capabilities.filters.search")}
        />
      </div>
    </div>
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

const PAGE_SIZE_OPTIONS = [10, 20, 50, 100] as const

function CapabilitiesPagination({
  page,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
}: {
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
  onPageSizeChange: (size: number) => void
}) {
  const { t } = useTranslation("admin")
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const safePage = Math.min(page, totalPages)
  const startIdx = total === 0 ? 0 : (safePage - 1) * pageSize + 1
  const endIdx = Math.min(safePage * pageSize, total)
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 px-1 text-sm text-fg-muted">
      <div>{t("capabilities.pagination.range", { start: startIdx, end: endIdx, total })}</div>
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1">
          <span>{t("capabilities.pagination.perPage")}</span>
          <select
            className="rounded border border-line bg-surface px-1.5 py-1 text-sm"
            value={pageSize}
            onChange={(e) => onPageSizeChange(Number(e.target.value))}
          >
            {PAGE_SIZE_OPTIONS.map((opt) => (
              <option key={opt} value={opt}>{opt}</option>
            ))}
          </select>
        </label>
        <Button size="sm" variant="outline" disabled={safePage <= 1} onClick={() => onPageChange(safePage - 1)}>
          {t("capabilities.pagination.prev")}
        </Button>
        <span>{t("capabilities.pagination.page", { page: safePage, totalPages })}</span>
        <Button size="sm" variant="outline" disabled={safePage >= totalPages} onClick={() => onPageChange(safePage + 1)}>
          {t("capabilities.pagination.next")}
        </Button>
      </div>
    </div>
  )
}


function CapabilityRowActions({
  capability,
  fromMarketplace,
  isAdmin,
  marketPending,
  uninstallPending,
  deletePending,
  onView,
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
  onView: () => void
  onAddVersion: () => void
  onMarketAction: (action: MarketCapabilityAction) => void
  onUninstall: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  const published = capability.visibility === "public" || capability.scope === "public"
  const disabledByRole = !isAdmin

  // Marketplace rows only support uninstall.
  if (fromMarketplace) {
    return (
      <RowActions>
        <ActionIconButton icon={Eye} label={t("capabilities.rowActions.view")} onClick={onView} />
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

  // Edit-as-new-version: clicking the primary action opens AddCapabilityVersionDialog,
  // which now carries name/description fields too. The old standalone Pencil
  // (PATCH-only metadata edit) was removed in favor of this single surface.
  const someMenuPending = marketPending || deletePending

  return (
    <RowActions>
      <ActionIconButton
        icon={Pencil}
        label={t("capabilities.rowActions.edit")}
        tone="primary"
        disabled={disabledByRole}
        onClick={onAddVersion}
      />
      <CapabilityRowMoreMenu
        published={published}
        disabledByRole={disabledByRole}
        menuPending={someMenuPending}
        onView={onView}
        onMarketAction={onMarketAction}
        onDelete={onDelete}
      />
    </RowActions>
  )
}

/**
 * "更多操作" menu for cross-workspace marketplace actions (publish,
 * deprecate). View-detail also lives here as a fallback entry point.
 */
function CapabilityRowMoreMenu({
  published,
  disabledByRole,
  menuPending,
  onView,
  onMarketAction,
  onDelete,
}: {
  published: boolean
  disabledByRole: boolean
  menuPending: boolean
  onView: () => void
  onMarketAction: (action: MarketCapabilityAction) => void
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          aria-label={t("capabilities.rowActions.more")}
          className="inline-flex h-7 w-7 items-center justify-center rounded-md text-fg-subtle hover:bg-surface-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-200 disabled:cursor-not-allowed disabled:opacity-50 data-[state=open]:bg-surface-muted"
        >
          {menuPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <MoreHorizontal className="h-3.5 w-3.5" />}
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={6}
          className="z-50 min-w-[180px] overflow-hidden rounded-md border border-line bg-surface p-1 text-sm text-fg-muted shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
        >
          <CapabilityMenuItem icon={Eye} label={t("capabilities.rowActions.view")} onSelect={onView} />

          {!disabledByRole && (
            <>
              <DropdownMenu.Separator className="my-1 h-px bg-surface-muted" />

              <CapabilityMenuItem
                icon={Share2}
                label={t(published ? "capabilities.rowActions.unpublish" : "capabilities.rowActions.publish")}
                tone={published ? "danger" : "success"}
                onSelect={() => onMarketAction(published ? "unpublish" : "publish")}
              />
              {/*
                "删除"释放 capability.name 的 workspace 唯一索引,允许重新
                导入同名能力。后端会拒绝仍被 agent 绑定的删除请求(409)。
                "作者下架"(deprecate)是另一回事——它表达"作者宣告维护停滞、
                已装机用户冻结版本",入口挪到了详情页的 marketplace 面板。
              */}
              <CapabilityMenuItem
                icon={Trash2}
                label={t("capabilities.rowActions.delete")}
                tone="danger"
                onSelect={onDelete}
              />
            </>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

/**
 * Single menu row inside CapabilityRowMoreMenu. Tone is purely visual —
 * the actual confirm/deny gates live in their respective dialogs (this is
 * just an entry point).
 */
function CapabilityMenuItem({
  icon: Icon,
  label,
  onSelect,
  tone = "default",
}: {
  icon: React.ComponentType<{ className?: string; strokeWidth?: number }>
  label: string
  onSelect: () => void
  tone?: "default" | "danger" | "success"
}) {
  const toneClass =
    tone === "danger"
      ? "text-danger data-[highlighted]:bg-danger-subtle data-[highlighted]:text-danger-emphasis"
      : tone === "success"
        ? "text-success data-[highlighted]:bg-success-subtle data-[highlighted]:text-success-emphasis"
        : "text-fg-muted data-[highlighted]:bg-surface-muted data-[highlighted]:text-fg"
  return (
    <DropdownMenu.Item
      onSelect={onSelect}
      className={`flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none ${toneClass}`}
    >
      <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
      <span>{label}</span>
    </DropdownMenu.Item>
  )
}

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
  const latestVersion = versionsQ.data?.versions?.[0]
  const installationSummary = useCapabilityEnabledAgents(wid, agentsQ.data?.agents ?? [], capability, versionsQ.data?.versions ?? [])
  const enabledCount = installationSummary.installations.length

  const fromMarketplace = new URLSearchParams(window.location.search).get("from") === "marketplace"

  if (fromMarketplace) {
    return <AdminLayout activeMenu="capabilities"><MarketplaceCapabilityDetail id={id} /></AdminLayout>
  }

  if (capQ.isLoading) {
    return <AdminLayout activeMenu="capabilities"><CapabilityDetailLoading /></AdminLayout>
  }

  if (capQ.error || !capability) {
    return (
      <AdminLayout activeMenu="capabilities">
        <EmptyState
          icon={Wrench}
          title={t("capabilities.detail.notFound.title")}
          description={capQ.error instanceof Error ? capQ.error.message : t("capabilities.detail.notFound.description")}
          action={<Button size="sm" variant="outline" onClick={() => navigateAdmin("capabilities")}>{t("capabilities.detail.backToList")}</Button>}
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
      const leakingVersion = (versionsQ.data?.versions ?? []).find((version) => containsPlaintextSecretPattern(JSON.stringify(version.content ?? {})))
      if (leakingVersion) {
        setMarketClientError(t("capabilities.errors.plaintextSecretPattern", { version: leakingVersion.version }))
        return
      }
    }
    setMarketAction(action)
  }

  return (
    <AdminLayout activeMenu="capabilities">
      <PageHeader
        backLink={<button onClick={() => navigateAdmin("capabilities")} className="inline-flex items-center gap-1 hover:text-fg hover:underline"><ArrowLeft className="h-3 w-3" />{t("capabilities.detail.backToList")}</button>}
        title={<span className="inline-flex items-center gap-2">{capability.name}<CapabilityTypeBadge type={capability.type} /></span>}
        description={capability.description || t("capabilities.detail.noDescription")}
        action={
          <>
            <Badge variant={capability.deprecated_at ? "neutral" : "success"} dot>{t(capability.deprecated_at ? "capabilities.status.deprecated" : "capabilities.status.active")}</Badge>
            {isAdmin && <Button size="sm" variant="outline" onClick={() => setEditOpen(true)}>{t("capabilities.actions.edit")}</Button>}
            {isAdmin && <Button size="sm" onClick={() => setAddVersionOpen(true)}><Plus className="h-3.5 w-3.5" />{t("capabilities.actions.addVersion")}</Button>}
          </>
        }
      />

      {toast && <ToastBanner message={toast} />}
      {marketClientError && <ErrorBanner message={marketClientError} />}

      <div className="space-y-4">
        <Card title={t("capabilities.detail.basic.title")}>
          <div className="grid gap-3 md:grid-cols-4">
            <DetailField label={t("capabilities.table.type")} value={<CapabilityTypeBadge type={capability.type} />} />
            <DetailField label={t("capabilities.table.credentials")} value={requiredCredentialsLabel(capability.required_credentials, i18n.language, t("capabilities.credentials.none"))} />
            <DetailField label={t("capabilities.detail.basic.createdAt")} value={formatDate(capability.created_at)} />
            <DetailField label={t("capabilities.table.latestVersion")} value={latestVersion?.version ?? t("capabilities.none")} mono />
          </div>
        </Card>

        <Card title={t("capabilities.versions.title")}>
          {versionsQ.isLoading ? (
            <div className="space-y-2">{Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
          ) : (versionsQ.data?.versions ?? []).length === 0 ? (
            <EmptyState icon={PackageCheck} title={t("capabilities.versions.empty.title")} description={t("capabilities.versions.empty.description")} />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("capabilities.versions.table.version")}</TableHead>
                  <TableHead>{t("capabilities.versions.table.createdAt")}</TableHead>
                  <TableHead>{t("capabilities.versions.table.enabledAgents")}</TableHead>
                  <TableHead>{t("capabilities.versions.table.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(versionsQ.data?.versions ?? []).map((version, index) => {
                  const count = installationSummary.versionCounts.get(version.id) ?? 0
                  return (
                    <TableRow key={version.id}>
                      <TableCell className="font-mono text-sm text-fg-muted">
                        {version.version} {index === 0 && <Badge className="ml-2" variant="neutral">{t("capabilities.versions.latest")}</Badge>}
                      </TableCell>
                      <TableCell className="text-sm text-fg-muted">{formatDate(version.created_at)}</TableCell>
                      <TableCell className="text-sm text-fg-muted">{count}</TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Button size="sm" variant="ghost" onClick={() => setViewVersion(version)}>
                            {t("capabilities.versions.viewContent.action")}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </Card>

        <Card title={t("capabilities.detail.enabledAgents.title", { count: enabledCount })}>
          {installationSummary.isLoading ? (
            <Skeleton className="h-12 w-full" />
          ) : enabledCount === 0 ? (
            <p className="text-sm text-fg-subtle">{t("capabilities.detail.enabledAgents.empty")}</p>
          ) : (
            <div className="space-y-2">
              {installationSummary.installations.map((item) => (
                <button key={item.agentID} type="button" onClick={() => navigateAdmin("agents", { id: item.agentID, tab: "capabilities" })} className="flex w-full items-center justify-between rounded-md border border-line p-3 text-left hover:bg-surface-subtle">
                  <span className="text-sm font-medium text-fg">{item.agentName}</span>
                  <span className="flex items-center gap-2 text-sm text-fg-subtle">
                    <span className="font-mono">{item.version}</span>
                    {!item.latest && <Badge variant="neutral">{t("capabilities.detail.enabledAgents.old")}</Badge>}
                    <ArrowUpRight className="h-3.5 w-3.5" />
                  </span>
                </button>
              ))}
            </div>
          )}
        </Card>

        {isAdmin && (
          <Card title={t("capabilities.marketStatus.title")}>
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={capability.visibility === "public" || capability.scope === "public" ? "success" : "neutral"} dot>
                    {capability.visibility === "public" || capability.scope === "public" ? t("capabilities.marketStatus.published") : t("capabilities.marketStatus.unpublished")}
                  </Badge>
                  {capability.deprecated_at && <Badge variant="destructive">{t("capabilities.deprecated.badgeSource")}</Badge>}
                </div>
                <p className="text-sm text-fg-subtle">{t("capabilities.marketStatus.installCount", { count: installCountQ.data ?? 0 })}</p>
              </div>
              <div className="flex flex-wrap gap-2">
                {/*
                  The deprecate / undeprecate toggle now applies to ALL
                  capabilities, not just published-marketplace ones —
                  it's the single "stop offering this" admin signal
                  since the standalone disable button was removed.
                  Existing agent bindings keep working either way;
                  the tooltip below makes that contract explicit so
                  admins don't fear they're about to break running
                  agents. Marketplace publish / unpublish remain a
                  separate concern.
                */}
                <Tooltip.Provider delayDuration={150}>
                  <Tooltip.Root>
                    <Tooltip.Trigger asChild>
                      <span>
                        <Button size="sm" variant="outline" onClick={() => requestMarketAction(capability.deprecated_at ? "undeprecate" : "deprecate")}>
                          {capability.deprecated_at ? t("capabilities.marketStatus.actions.undeprecate") : t("capabilities.marketStatus.actions.deprecate")}
                        </Button>
                      </span>
                    </Tooltip.Trigger>
                    <Tooltip.Portal>
                      <Tooltip.Content className="z-50 max-w-xs rounded-md border border-line bg-surface px-2 py-1 text-sm text-fg-muted shadow-md">
                        {capability.deprecated_at ? t("capabilities.marketStatus.undeprecateTooltip") : t("capabilities.marketStatus.deprecateTooltip")}
                        <Tooltip.Arrow className="fill-white" />
                      </Tooltip.Content>
                    </Tooltip.Portal>
                  </Tooltip.Root>
                </Tooltip.Provider>
                {(capability.visibility === "public" || capability.scope === "public") ? (
                  <Button size="sm" variant="ghost" onClick={() => requestMarketAction("unpublish")}>{t("capabilities.marketStatus.actions.unpublish")}</Button>
                ) : (
                  <Button size="sm" onClick={() => requestMarketAction("publish")}>{t("capabilities.marketStatus.actions.publish")}</Button>
                )}
              </div>
            </div>
          </Card>
        )}
      </div>

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

export function CapabilityTypeBadge({ type }: { type: Capability["type"] }) {
  if (type === "skill") return <Badge variant="primary">Skill</Badge>
  if (type === "plugin") return <Badge variant="success">Plugin</Badge>
  if (type === "system_prompt") return <Badge variant="warning">System Prompt</Badge>
  return <Badge variant="neutral">MCP</Badge>
}

/**
 * EditCapabilityDialog — minimal name + description editor.
 *
 * The pre-M4 page used CreateCapabilityDialog in mode="edit" for this, which
 * dragged in the full create form just to disable most of it. Now that the
 * create path goes through the import flow, this dialog is small enough to
 * inline.
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
          <FormField label={t("capabilities.fields.name.label")} help={t("capabilities.fields.name.help")} required>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("capabilities.fields.name.placeholder")} />
          </FormField>
          <FormField label={t("capabilities.fields.description.label")}>
            <Input value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t("capabilities.fields.description.placeholder")} />
          </FormField>
          {(validationError || errMsg) && <ErrorBanner message={validationError ?? errMsg ?? ""} />}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={pending}>{t("capabilities.actions.cancel")}</Button>
          <Button size="sm" disabled={pending || !!validationError} onClick={() => onSubmit({ name: trimmedName, description: description.trim() })}>
            {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("capabilities.actions.save")}
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
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>{t("capabilities.actions.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type Translate = ReturnType<typeof useTranslation<"admin">>["t"]

/**
 * Picks the right body for "view version content":
 *   - mcp + canonical_spec present → pretty-print canonical_spec.mcp (the
 *     M3-and-onward format that the version-import flow writes)
 *   - mcp without canonical_spec  → fall back to legacy `content` (pre-M3
 *     versions that pre-date the import flow)
 *   - skill + canonical_spec      → render parsed slug/title/description/
 *     instruction/trigger from canonical_spec.skill
 *   - skill without canonical_spec→ legacy git_repo_url/git_ref/path layout
 *
 * The fallbacks keep old versions readable after we drop the legacy add-version
 * write path; the canonical_spec branch is what every NEW version will hit.
 */
function renderViewVersionBody(version: CapabilityVersion, capability: Capability, t: Translate): React.ReactNode {
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
      <div className="space-y-2 rounded-md border border-line bg-surface-subtle p-3">
        <DetailField label="mode" value={sp?.mode ?? "append"} mono />
        <DetailField
          label="prompt"
          value={
            <pre className="whitespace-pre-wrap font-mono text-sm leading-relaxed text-fg-muted">
              {sp?.prompt ?? t("capabilities.none")}
            </pre>
          }
        />
      </div>
    )
  }

  if (capability.type === "mcp") {
    if (canonicalSpec?.mcp) {
      return (
        <pre className="max-h-[420px] overflow-y-auto whitespace-pre-wrap break-all rounded-md border border-line bg-surface-subtle p-3 font-mono text-sm leading-relaxed text-fg-muted">
          {JSON.stringify(canonicalSpec.mcp, null, 2)}
        </pre>
      )
    }
    return (
      <pre className="max-h-[420px] overflow-y-auto whitespace-pre-wrap break-all rounded-md border border-line bg-surface-subtle p-3 font-mono text-sm leading-relaxed text-fg-muted">
        {JSON.stringify(version.content ?? {}, null, 2)}
      </pre>
    )
  }

  if (capability.type === "plugin") {
    const plugin = canonicalSpec?.plugin
    return (
      <div className="space-y-2 rounded-md border border-line bg-surface-subtle p-3">
        {plugin?.name && <DetailField label="name" value={plugin.name} mono />}
        {plugin?.version && <DetailField label="version" value={plugin.version} mono />}
        {plugin?.description && <DetailField label="description" value={plugin.description} />}
        {plugin?.author && <DetailField label="author" value={plugin.author} />}
        {plugin?.upload_source && <DetailField label="upload_source" value={plugin.upload_source} mono />}
        {plugin?.oss_key && <DetailField label="oss_key" value={plugin.oss_key} mono />}
        {plugin?.sha256 && <DetailField label="sha256" value={plugin.sha256} mono />}
        {plugin?.github_repo && <DetailField label="github_repo" value={plugin.github_repo} mono />}
        {!plugin && (
          <p className="text-sm text-fg-subtle">{t("capabilities.none")}</p>
        )}
      </div>
    )
  }

  // skill
  if (canonicalSpec?.skill) {
    const skill = canonicalSpec.skill
    return (
      <div className="space-y-2 rounded-md border border-line bg-surface-subtle p-3">
        {skill.slug && <DetailField label="slug" value={skill.slug} mono />}
        {skill.title && <DetailField label="title" value={skill.title} />}
        {skill.description && <DetailField label="description" value={skill.description} />}
        {skill.trigger && <DetailField label="trigger" value={skill.trigger} />}
        {skill.instruction && (
          <DetailField
            label="instruction"
            value={<pre className="whitespace-pre-wrap font-mono text-sm leading-relaxed text-fg-muted">{skill.instruction}</pre>}
          />
        )}
      </div>
    )
  }
  return (
    <div className="space-y-2 rounded-md border border-line bg-surface-subtle p-3">
      <DetailField label={t("capabilities.fields.gitRepoUrl.label")} value={version.git_repo_url || t("capabilities.none")} mono />
      <DetailField label={t("capabilities.fields.gitRef.label")} value={skillVersionRef(version) || t("capabilities.none")} mono />
      <DetailField label={t("capabilities.fields.path.label")} value={version.path || t("capabilities.none")} mono />
    </div>
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

function FormField({ label, help, required, children }: { label: string; help?: string; required?: boolean; children: React.ReactNode }) {
  return <label className="grid gap-1.5"><span className="text-sm font-medium text-fg-muted">{label}{required && <span className="text-danger"> *</span>}</span>{children}{help && <span className="text-xs leading-relaxed text-fg-subtle">{help}</span>}</label>
}

function DetailField({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return <div className="rounded-md border border-line bg-surface p-3"><p className="text-xs text-fg-subtle">{label}</p><div className={`mt-1 text-sm text-fg ${mono ? "font-mono" : ""}`}>{value}</div></div>
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return <section className="rounded-lg border border-line bg-surface p-4"><h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-fg-subtle">{title}</h3>{children}</section>
}

function ErrorBanner({ message }: { message: string }) {
  return <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis" role="alert">{message}</div>
}

function ToastBanner({ message }: { message: string }) {
  return <div className="mb-4 rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">{message}</div>
}

function CapabilitiesLoading() {
  return <div className="space-y-2 rounded-lg border border-line bg-surface p-4">{Array.from({ length: 5 }).map((_, i) => <div key={i} className="flex items-center gap-3"><Wrench className="h-4 w-4 text-fg-faint" /><Skeleton className="h-5 flex-1" /></div>)}</div>
}

function CapabilityDetailLoading() {
  return <div className="space-y-4"><Skeleton className="h-16 w-full" /><Skeleton className="h-28 w-full" /><Skeleton className="h-40 w-full" /></div>
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString() : "—"
}
