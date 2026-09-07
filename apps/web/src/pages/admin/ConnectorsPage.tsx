import { useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { ArrowUpRight, Cable, Search } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { DetailRail, RailLayout, RailSection } from "../../components/ui/detail-rail"
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
  const { navigate, entityId } = useAdminView()
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
  // Hold the type through the rail's exit so closing animates instead of
  // vanishing — the same pattern every ledger uses.
  const [railID, setRailID] = useState<string | null>(entityId)
  if (entityId && entityId !== railID) setRailID(entityId)
  const railConnector = connectors.find((c) => c.connector_type === railID) ?? null

  return (
    <AdminLayout activeMenu="settings" fullBleed>
      <RailLayout
        rail={
          railID ? (
            <ConnectorRail
              connector={railConnector}
              loading={query.isLoading}
              open={!!entityId}
              onClose={() => navigate("connectors")}
              onClosed={() => setRailID(null)}
              onViewAgents={() => navigate("agents")}
            />
          ) : null
        }
      >
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
              <ConnectorRow
                key={c.connector_type}
                connector={c}
                selected={c.connector_type === entityId}
                onSelect={() => navigate("connectors", { id: c.connector_type === entityId ? null : c.connector_type })}
              />
            ))}
          </ul>
        </Ledger>
      )}
      </div>
      </RailLayout>
    </AdminLayout>
  )
}

function ConnectorRow({ connector, selected, onSelect }: { connector: ConnectorSummary; selected: boolean; onSelect: () => void }) {
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onSelect()
    }
  }
  return (
    <LedgerRow selected={selected} onClick={onSelect} onKeyDown={onKeyDown} className="cursor-pointer">
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

/**
 * A connector type read in the rail beside the list. It is a summary, not a
 * workplace: what it is, whether it is ready, and who is on it.
 */
function ConnectorRail({ connector, loading, open, onClose, onClosed, onViewAgents }: {
  connector: ConnectorSummary | null
  loading: boolean
  open: boolean
  onClose: () => void
  onClosed: () => void
  onViewAgents: () => void
}) {
  const { t } = useTranslation("admin")

  if (!connector) {
    return (
      <DetailRail
        open={open}
        onClose={onClose}
        onClosed={onClosed}
        aria-label={t("connectors.page.title")}
        header={<Skeleton className="h-3 w-40" />}
      >
        {loading ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-3 w-full" />)}
          </div>
        ) : (
          <EmptyState
            icon={Cable}
            title={t("connectors.detail.notFound.title")}
            description={t("connectors.detail.notFound.description")}
          />
        )}
      </DetailRail>
    )
  }

  return (
    <DetailRail
      open={open}
      onClose={onClose}
      onClosed={onClosed}
      aria-label={connector.label}
      header={
        <>
          <span className="min-w-0 truncate text-sm font-medium text-fg">{connector.label}</span>
          <Badge className="font-mono">{connector.connector_type}</Badge>
        </>
      }
      footer={
        connector.agent_count > 0 ? (
          <Button variant="link" className="ml-auto" onClick={onViewAgents}>
            {t("connectors.detail.agentSummary", { count: connector.agent_count })}
            <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
          </Button>
        ) : undefined
      }
    >
      <RailSection title={t("connectors.detail.tabs.overview")}>
        <PropertyList>
          <Property label={t("connectors.detail.status")}>
            <ConnectorStatus status={connector.status} />
          </Property>
          <Property label={t("connectors.detail.type")} mono>{connector.connector_type}</Property>
          <Property label={t("connectors.detail.agentCount")} mono>{connector.agent_count}</Property>
        </PropertyList>
      </RailSection>

      <RailSection title={t("connectors.detail.agents")} meta={connector.agent_count || undefined} className="mt-6">
        {connector.agent_count === 0 ? (
          <p className="pt-1 text-sm text-fg-muted">{t("connectors.detail.noAgents")}</p>
        ) : (
          <ul className="m-0 list-none p-0">
            {connector.agent_slugs.map((slug) => (
              <li key={slug} className="flex h-8 items-center gap-1.5 border-b border-line text-sm text-fg">
                <InitialTile name={slug} />
                <span className="truncate font-medium">{slug}</span>
              </li>
            ))}
          </ul>
        )}
      </RailSection>
    </DetailRail>
  )
}
