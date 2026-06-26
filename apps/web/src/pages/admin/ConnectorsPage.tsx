import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  ArrowUpRight,
  Bot,
  Cable,
  Search,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Skeleton } from "../../components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../components/ui/table"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useProjectConnectors } from "../../lib/api-registry"
import type { ConnectorSummary } from "../../lib/api-types"
import { useProjectId } from "../../lib/workspace"

/* ------------------------------------------------------------------ */
/*  Status badge                                                       */
/* ------------------------------------------------------------------ */

function ConnectorStatusBadge({ status }: { status: ConnectorSummary["status"] }) {
  const { t } = useTranslation("admin")
  switch (status) {
    case "ready":
      return <Badge variant="success" dot>{t("connectors.status.ready")}</Badge>
    case "needs_config":
      return <Badge variant="warning" dot>{t("connectors.status.needsConfig")}</Badge>
    case "offline":
      return <Badge variant="destructive" dot>{t("connectors.status.offline")}</Badge>
    default:
      return <Badge variant="neutral">{t("connectors.status.unknown")}</Badge>
  }
}

/* ------------------------------------------------------------------ */
/*  List page                                                          */
/* ------------------------------------------------------------------ */

export function ConnectorsPage() {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const pid = useProjectId()
  const [keyword, setKeyword] = useState("")

  const query = useProjectConnectors(pid)
  const connectors = useMemo(() => query.data?.connectors ?? [], [query.data])
  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  const filtered = connectors.filter((c) => {
    if (!keyword) return true
    const q = keyword.toLowerCase()
    return c.connector_type.toLowerCase().includes(q) || c.label.toLowerCase().includes(q)
  })

  return (
    <AdminLayout activeMenu="connectors">
      <PageHeader
        title={t("connectors.page.title")}
        description={t("connectors.page.description")}
      />

      <p className="mb-4 rounded-lg border border-dashed border-slate-200 bg-slate-50/60 p-3 text-[12px] leading-relaxed text-slate-600">
        {t("connectors.aggregateHint")}
      </p>
      {!pid ? (
        <ScopeRequiredState scope="project" resourceName={t("connectors.page.title")} />
      ) : query.isLoading ? (
        <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-4">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-12 w-full" />)}
        </div>
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
      ) : (
        <div className="space-y-4">
          <div className="flex items-center justify-between gap-3">
            <span className="text-[12px] text-slate-500">
              {t("connectors.summary", { count: connectors.length })}
            </span>
            <div className="relative w-72">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-400" strokeWidth={1.75} />
              <Input
                placeholder={t("connectors.search.placeholder")}
                className="pl-8 text-xs"
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
              />
            </div>
          </div>

          {filtered.length === 0 ? (
            <EmptyState
              icon={Cable}
              title={t("connectors.empty.title")}
              description={t("connectors.empty.description")}
            />
          ) : (
            <div className="overflow-hidden rounded-lg border border-slate-200 bg-white">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("connectors.table.connector")}</TableHead>
                    <TableHead>{t("connectors.table.status")}</TableHead>
                    <TableHead className="text-right">{t("connectors.table.agentCount")}</TableHead>
                    <TableHead className="text-right pr-4">{t("connectors.table.agents")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((c) => (
                    <TableRow
                      key={c.connector_type}
                      className="cursor-pointer"
                      onClick={() => navigate("connectors", { id: c.connector_type })}
                    >
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Cable className="h-3.5 w-3.5 shrink-0 text-slate-400" strokeWidth={1.75} />
                          <span className="text-[14px] font-medium text-slate-900">{c.label}</span>
                          <code className="text-[11px] text-slate-400">{c.connector_type}</code>
                        </div>
                      </TableCell>
                      <TableCell><ConnectorStatusBadge status={c.status} /></TableCell>
                      <TableCell className="text-right text-[12px] tabular-nums text-slate-700">{c.agent_count}</TableCell>
                      <TableCell className="text-right pr-4">
                        <span className="text-[11px] text-slate-500">
                          {c.agent_count === 0 ? "—" : t("connectors.table.agentSummary", { count: c.agent_count })}
                        </span>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      )}
    </AdminLayout>
  )
}

/* ------------------------------------------------------------------ */
/*  Detail page (per-type summary)                                    */
/* ------------------------------------------------------------------ */

export function ConnectorDetailPage({ id }: { id: string }) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const pid = useProjectId()

  const query = useProjectConnectors(pid)
  const connector = (query.data?.connectors ?? []).find((c) => c.connector_type === id)

  if (query.isLoading) {
    return (
      <AdminLayout activeMenu="connectors">
        <Skeleton className="h-64 w-full" />
      </AdminLayout>
    )
  }

  if (!connector) {
    return (
      <AdminLayout activeMenu="connectors">
        <PageHeader
          backLink={
            <button onClick={() => navigate("connectors")} className="hover:underline">
              ← {t("connectors.page.title")}
            </button>
          }
          title={id}
        />
        <EmptyState
          icon={Cable}
          title={t("connectors.detail.notFound.title")}
          description={t("connectors.detail.notFound.description")}
        />
      </AdminLayout>
    )
  }

  return (
    <AdminLayout activeMenu="connectors">
      <PageHeader
        backLink={
          <button onClick={() => navigate("connectors")} className="hover:text-slate-900 hover:underline">
            ← {t("connectors.page.title")}
          </button>
        }
        title={connector.label}
        description={<code className="font-mono text-[12px]">{connector.connector_type}</code>}
        action={<ConnectorStatusBadge status={connector.status} />}
      />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
        <Stat label={t("connectors.detail.agentCount")} value={String(connector.agent_count)} />
        <Stat label={t("connectors.detail.status")} value={connector.status} mono />
        <Stat label={t("connectors.detail.type")} value={connector.connector_type} mono />
      </div>

      <section className="mt-6 rounded-lg border border-slate-200 bg-white p-4">
        <h3 className="mb-3 text-[12px] font-semibold uppercase tracking-wider text-slate-500">
          {t("connectors.detail.agents")}
        </h3>
        {connector.agent_count === 0 ? (
          <p className="text-[12px] text-slate-500">{t("connectors.detail.noAgents")}</p>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => navigate("agents")}
            className="justify-start px-0"
          >
            <Bot className="h-3.5 w-3.5 text-slate-400" strokeWidth={1.75} />
            {t("connectors.detail.agentSummary", { count: connector.agent_count })}
            <ArrowUpRight className="ml-auto h-3 w-3 text-slate-400" />
          </Button>
        )}
      </section>
    </AdminLayout>
  )
}

function Stat({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-lg border border-slate-200 bg-white p-4">
      <div className="text-[11px] uppercase tracking-wider text-slate-400">{label}</div>
      <div className={`mt-1 text-[20px] font-semibold tabular-nums text-slate-900 ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  )
}
