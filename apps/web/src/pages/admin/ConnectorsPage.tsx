import { useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { ArrowLeft, ArrowUpRight, Cable, Search } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { DetailHeading } from "../../components/ui/section"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { InitialTile, Ledger, LedgerHeader, LedgerNum, LedgerRow, col } from "../../components/ui/ledger"
import { PropertyList, Property } from "../../components/ui/property-list"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon, type StatusKind } from "../../components/ui/status-icon"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useWorkspaceConnectors } from "../../lib/api-registry"
import type { ConnectorSummary } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"

/* ------------------------------------------------------------------ */
/*  Status                                                             */
/* ------------------------------------------------------------------ */

type ConnectorStatus = "ready" | "needs_config" | "offline" | "unknown"

const STATUS_ICON: Record<ConnectorStatus, StatusKind> = {
  ready: "completed",
  needs_config: "queued",
  offline: "failed",
  unknown: "interrupted",
}

const STATUS_KEY: Record<ConnectorStatus, "ready" | "needsConfig" | "offline" | "unknown"> = {
  ready: "ready",
  needs_config: "needsConfig",
  offline: "offline",
  unknown: "unknown",
}

function normalizeStatus(status: ConnectorSummary["status"]): ConnectorStatus {
  return status === "ready" || status === "needs_config" || status === "offline" ? status : "unknown"
}

/** 14px status icon and the status word in ink. */
function ConnectorStatus({ status }: { status: ConnectorSummary["status"] }) {
  const { t } = useTranslation("admin")
  const s = normalizeStatus(status)
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <StatusIcon status={STATUS_ICON[s]} />
      <span className="truncate">{t(`connectors.status.${STATUS_KEY[s]}`)}</span>
    </span>
  )
}

/* ------------------------------------------------------------------ */
/*  List page                                                          */
/* ------------------------------------------------------------------ */

/** connector (label + type chip) · status · agents */
const LEDGER_COLUMNS = [col.title(), col.meta(140), col.num(72)]

export function ConnectorsPage() {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const wsId = useWorkspaceId()
  const [keyword, setKeyword] = useState("")

  const query = useWorkspaceConnectors(wsId)
  const connectors = useMemo(() => query.data?.connectors ?? [], [query.data])
  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  const filtered = connectors.filter((c) => {
    if (!keyword) return true
    const q = keyword.toLowerCase()
    return c.connector_type.toLowerCase().includes(q) || c.label.toLowerCase().includes(q)
  })

  const pageTitle = t("connectors.page.title")

  return (
    <AdminLayout activeMenu="settings" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader
        className="static mx-0 mb-0"
        title={pageTitle}
        subtitleFor="connectors.page.title"
        action={
          <>
            <SettingsTabs active="connectors" />
          {wsId ? (
            <div className="relative w-72">
              <Search
                className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
                strokeWidth={1.5}
                aria-hidden="true"
              />
              <Input
                type="search"
                placeholder={t("connectors.search.placeholder")}
                aria-label={t("connectors.search.placeholder")}
                className="pl-7"
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
              />
            </div>
          ) : undefined}
          </>
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-10">
      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={pageTitle} />
      ) : query.isLoading ? (
        <ConnectorsSkeleton />
      ) : err ? (
        <ErrorState
          title={isUnreachable ? t("connectors.loadError.unreachable.title") : t("connectors.loadError.title")}
          description={
            isUnreachable
              ? t("connectors.loadError.unreachable.description")
              : err instanceof Error
                ? err.message
                : t("connectors.loadError.description")
          }
          hint={isUnreachable ? t("connectors.loadError.unreachable.hint") : t("connectors.loadError.hint")}
          onRetry={() => void query.refetch()}
        />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={Cable}
          title={t("connectors.empty.title")}
          description={t("connectors.empty.description")}
        />
      ) : (
        <Ledger columns={LEDGER_COLUMNS} className="-mx-6" role="listbox" aria-label={pageTitle}>
          <LedgerHeader>
            <span>{t("connectors.table.connector")}</span>
            <span>{t("connectors.table.status")}</span>
            <span className="text-right">{t("connectors.table.agentCount")}</span>
          </LedgerHeader>
          <ul className="m-0 list-none p-0">
            {filtered.map((c) => (
              <ConnectorRow key={c.connector_type} connector={c} onSelect={() => navigate("connectors", { id: c.connector_type })} />
            ))}
          </ul>
        </Ledger>
      )}
      </div>
      </div>
    </AdminLayout>
  )
}

function ConnectorRow({ connector, onSelect }: { connector: ConnectorSummary; onSelect: () => void }) {
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onSelect()
    }
  }
  return (
    <LedgerRow onClick={onSelect} onKeyDown={onKeyDown} className="cursor-pointer">
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate font-medium">{connector.label}</span>
        <Badge className="font-mono">{connector.connector_type}</Badge>
      </span>
      <ConnectorStatus status={connector.status} />
      <LedgerNum muted={connector.agent_count === 0}>{connector.agent_count}</LedgerNum>
    </LedgerRow>
  )
}

function ConnectorsSkeleton() {
  return (
    <div className="-mx-6 -mt-1">
      <div className="h-7 border-b border-line" />
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line px-4">
          <Skeleton className="h-3 w-40" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-10" />
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Detail page (per-type summary)                                    */
/* ------------------------------------------------------------------ */

export function ConnectorDetailPage({ id }: { id: string }) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const wsId = useWorkspaceId()

  const query = useWorkspaceConnectors(wsId)
  const connector = (query.data?.connectors ?? []).find((c) => c.connector_type === id)

  const back = (
    <button
      type="button"
      onClick={() => navigate("connectors")}
      className="inline-flex items-center gap-1 rounded text-xs text-fg-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
    >
      <ArrowLeft className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
      {t("connectors.page.title")}
    </button>
  )

  if (query.isLoading) {
    return (
      <AdminLayout activeMenu="settings">
        <PageHeader backLink={back} action={<SettingsTabs active="connectors" />} />
        <div className="space-y-3">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-3 w-64" />
          ))}
        </div>
      </AdminLayout>
    )
  }

  if (!connector) {
    return (
      <AdminLayout activeMenu="settings">
        <PageHeader backLink={back} action={<SettingsTabs active="connectors" />} />
        <EmptyState
          icon={Cable}
          title={t("connectors.detail.notFound.title")}
          description={t("connectors.detail.notFound.description")}
        />
      </AdminLayout>
    )
  }

  return (
    <AdminLayout activeMenu="settings">
      <PageHeader backLink={back} action={<SettingsTabs active="connectors" />} />

      <DetailHeading
        title={connector.label}
        badges={<span className="font-mono text-xs text-fg-muted">{connector.connector_type}</span>}
      />

      <Tabs defaultValue="overview" className="max-w-2xl">
        <TabsList>
          <TabsTrigger value="overview">{t("connectors.detail.tabs.overview")}</TabsTrigger>
          <TabsTrigger value="agents">
            {t("connectors.detail.agents")}
            <span className="tabular-nums text-fg-muted">{connector.agent_count}</span>
          </TabsTrigger>
        </TabsList>

        <TabsContent value="overview">
          <PropertyList>
            <Property label={t("connectors.detail.status")}>
              <ConnectorStatus status={connector.status} />
            </Property>
            <Property label={t("connectors.detail.type")} mono>{connector.connector_type}</Property>
            <Property label={t("connectors.detail.agentCount")} mono>{connector.agent_count}</Property>
          </PropertyList>
        </TabsContent>

        <TabsContent value="agents">
          {connector.agent_count === 0 ? (
            <p className="text-sm text-fg-muted">{t("connectors.detail.noAgents")}</p>
          ) : (
            <>
              <ul className="m-0 list-none p-0">
                {connector.agent_slugs.map((slug) => (
                  <li key={slug} className="flex h-8 items-center gap-1.5 border-b border-line text-sm text-fg">
                    <InitialTile name={slug} />
                    <span className="truncate font-medium">{slug}</span>
                  </li>
                ))}
              </ul>
              <div className="flex justify-end pt-3">
                <Button variant="link" onClick={() => navigate("agents")}>
                  {t("connectors.detail.agentSummary", { count: connector.agent_count })}
                  <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
                </Button>
              </div>
            </>
          )}
        </TabsContent>
      </Tabs>
    </AdminLayout>
  )
}
