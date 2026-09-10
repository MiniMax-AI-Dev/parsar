import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { LineChart as LineChartIcon } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Ledger, LedgerHeader, LedgerNum, LedgerRow, col } from "../../components/ui/ledger"
import { Property, PropertyList } from "../../components/ui/property-list"
import { Skeleton } from "../../components/ui/skeleton"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useUsage } from "../../lib/api-governance"
import type { UsageLog } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { SectionHead } from "../../components/ui/section"
import { knownUsageCost } from "../../lib/usage-cost"

/* ------------------------------------------------------------------ */
/*  Aggregation                                                        */
/* ------------------------------------------------------------------ */

interface UsageSummary {
  runs: Set<string>
  inputTokens: number
  outputTokens: number
  costUsd: number
  unknownCosts: number
}

function summarize(logs: UsageLog[]): UsageSummary {
  const summary: UsageSummary = {
    runs: new Set(),
    inputTokens: 0,
    outputTokens: 0,
    costUsd: 0,
    unknownCosts: 0,
  }
  for (const u of logs) {
    if (u.agent_run_id) summary.runs.add(u.agent_run_id)
    summary.inputTokens += u.input_tokens ?? 0
    summary.outputTokens += u.output_tokens ?? 0
    const cost = knownUsageCost(u.cost_usd)
    summary.costUsd += cost ?? 0
    if (cost === null) summary.unknownCosts += 1
  }
  return summary
}

interface ByModelRow {
  key: string
  provider: string
  model: string
  inputTokens: number
  outputTokens: number
  costUsd: number
  unknownCosts: number
  callCount: number
}

function groupByModel(logs: UsageLog[]): ByModelRow[] {
  const map = new Map<string, ByModelRow>()
  for (const u of logs) {
    const key = `${u.provider}::${u.model}`
    let row = map.get(key)
    if (!row) {
      row = {
        key,
        provider: u.provider,
        model: u.model,
        inputTokens: 0,
        outputTokens: 0,
        costUsd: 0,
        unknownCosts: 0,
        callCount: 0,
      }
      map.set(key, row)
    }
    row.inputTokens += u.input_tokens ?? 0
    row.outputTokens += u.output_tokens ?? 0
    const cost = knownUsageCost(u.cost_usd)
    row.costUsd += cost ?? 0
    if (cost === null) row.unknownCosts += 1
    row.callCount += 1
  }
  return [...map.values()].sort((a, b) => b.costUsd - a.costUsd)
}

function shortId(s: string | undefined | null, n = 10): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(0, n) + "…"
}

function fmtUsd(cost: number): string {
  if (cost < 0.001) return `$${cost.toFixed(5)}`
  if (cost < 1) return `$${cost.toFixed(4)}`
  return `$${cost.toFixed(2)}`
}

function UsageCost({ value, partial = false }: { value: number; partial?: boolean }) {
  const { t } = useTranslation("admin")
  const cost = knownUsageCost(value)
  if (cost === null) return <span className="font-sans text-fg-muted">{t("usage.cost.unknown")}</span>
  const amount = fmtUsd(cost)
  return <span title={partial ? t("usage.cost.partial") : undefined} aria-label={partial ? `${amount} · ${t("usage.cost.partial")}` : undefined}>{amount}{partial ? "*" : ""}</span>
}

function fmtInt(n: number): string {
  return n.toLocaleString()
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, { hour12: false })
}

const METRIC_COLUMNS = [col.num(104, 0), col.num(104, 0), col.num(96, 0)]
/** provider · model · calls · input · output · cost */
const MODEL_COLUMNS = [col.meta(0), col.id(0, 2), col.num(72, 0), ...METRIC_COLUMNS]
/** time · run · provider · model · input · output · cost */
const RECENT_COLUMNS = [col.age(0, 1), col.id(0, 1), col.meta(0), col.id(0, 1.2), ...METRIC_COLUMNS]

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export function UsagePage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const { navigate } = useAdminView()

  const query = useUsage(wsId)
  const logs = useMemo(() => query.data?.usage_logs ?? [], [query.data])
  const summary = useMemo(() => summarize(logs), [logs])
  const byModel = useMemo(() => groupByModel(logs), [logs])

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable
  const pageTitle = t("usage.page.title")

  return (
    <AdminLayout activeMenu="usage" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={pageTitle}
          subtitleFor="usage.page.title"
        />
        {!wsId ? (
          <ScopeRequiredState scope="workspace" resourceName={pageTitle} />
        ) : query.isLoading ? (
          <UsageLoadingSkeleton />
        ) : err ? (
          <div className="px-4 pt-4">
            <ErrorState
              title={isUnreachable ? t("usage.loadError.unreachable.title") : t("usage.loadError.title")}
              description={isUnreachable ? t("usage.loadError.unreachable.description") : t("usage.loadError.description")}
              detail={!isUnreachable && err instanceof Error ? err.message : undefined}
              hint={isUnreachable ? t("usage.loadError.unreachable.hint") : t("usage.loadError.hint")}
              onRetry={() => void query.refetch()}
            />
          </div>
        ) : logs.length === 0 ? (
          <EmptyState
            icon={LineChartIcon}
            title={t("usage.empty.title")}
            description={t("usage.empty.description")}
          />
        ) : (
          <div className="min-h-0 flex-1 overflow-y-auto pb-10">
            <PropertyList className="px-6 pt-3">
              <Property label={t("usage.stats.runs")} mono className="tabular-nums">{fmtInt(summary.runs.size)}</Property>
              <Property label={t("usage.stats.inputTokens")} mono className="tabular-nums">{fmtInt(summary.inputTokens)}</Property>
              <Property label={t("usage.stats.outputTokens")} mono className="tabular-nums">{fmtInt(summary.outputTokens)}</Property>
              <Property label={t("usage.stats.cost")} mono className="tabular-nums"><UsageCost value={summary.costUsd} partial={summary.unknownCosts > 0} /></Property>
            </PropertyList>
            {summary.unknownCosts > 0 && (
              <p className="mt-3 px-6 text-xs text-fg-muted">{t("usage.cost.incomplete")}</p>
            )}

            <SectionHead title={t("usage.byModel.title")} className="mt-6 px-6" />
            <Ledger columns={MODEL_COLUMNS} className="flex-none overflow-visible" role="list" aria-label={t("usage.byModel.title")}>
              <LedgerHeader>
                <span>{t("usage.byModel.provider")}</span>
                <span>{t("usage.byModel.model")}</span>
                <span className="text-right">{t("usage.byModel.calls")}</span>
                <span className="text-right">{t("usage.byModel.input")}</span>
                <span className="text-right">{t("usage.byModel.output")}</span>
                <span className="text-right">{t("usage.byModel.cost")}</span>
              </LedgerHeader>
              <ul className="m-0 list-none p-0">
                {byModel.map((m) => (
                  <LedgerRow key={m.key} role="listitem" tabIndex={-1}>
                    <span className="truncate text-xs text-fg-muted" title={m.provider}>{m.provider || "—"}</span>
                    <span className="truncate font-mono text-xs text-fg" title={m.model}>{m.model || "—"}</span>
                    <LedgerNum>{fmtInt(m.callCount)}</LedgerNum>
                    <LedgerNum>{fmtInt(m.inputTokens)}</LedgerNum>
                    <LedgerNum>{fmtInt(m.outputTokens)}</LedgerNum>
                    <LedgerNum><UsageCost value={m.costUsd} partial={m.unknownCosts > 0} /></LedgerNum>
                  </LedgerRow>
                ))}
              </ul>
            </Ledger>

            <SectionHead title={t("usage.recent.title")} className="mt-6 px-6" />
            <Ledger columns={RECENT_COLUMNS} className="flex-none overflow-visible" role="list" aria-label={t("usage.recent.title")}>
              <LedgerHeader>
                <span>{t("usage.recent.time")}</span>
                <span>{t("usage.recent.run")}</span>
                <span>{t("usage.recent.provider")}</span>
                <span>{t("usage.recent.model")}</span>
                <span className="text-right">{t("usage.recent.input")}</span>
                <span className="text-right">{t("usage.recent.output")}</span>
                <span className="text-right">{t("usage.recent.cost")}</span>
              </LedgerHeader>
              <ul className="m-0 list-none p-0">
                {logs.map((u) => (
                  <LedgerRow key={u.id} role="listitem" tabIndex={-1}>
                    <span className="truncate font-mono text-xs tabular-nums text-fg-muted" title={fmtTime(u.created_at)}>{fmtTime(u.created_at)}</span>
                    {u.agent_run_id ? (
                      <button
                        type="button"
                        className="truncate text-left font-mono text-xs text-fg-muted underline-offset-4 hover:text-fg hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
                        onClick={() => navigate("runs", { id: u.agent_run_id! })}
                        title={u.agent_run_id}
                      >
                        {shortId(u.agent_run_id, 18)}
                      </button>
                    ) : (
                      <span className="text-xs text-fg-muted" title={t("usage.recent.noRun")}>—</span>
                    )}
                    <span className="truncate text-xs text-fg-muted" title={u.provider}>{u.provider || "—"}</span>
                    <span className="truncate font-mono text-xs text-fg" title={u.model}>{u.model || "—"}</span>
                    <LedgerNum>{fmtInt(u.input_tokens)}</LedgerNum>
                    <LedgerNum>{fmtInt(u.output_tokens)}</LedgerNum>
                    <LedgerNum><UsageCost value={u.cost_usd} /></LedgerNum>
                  </LedgerRow>
                ))}
              </ul>
            </Ledger>
          </div>
        )}
      </div>
    </AdminLayout>
  )
}

function UsageLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="space-y-3 pb-6">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="flex h-4 items-center gap-3">
            <Skeleton className="h-3 w-28" />
            <Skeleton className="h-3 w-20" />
          </div>
        ))}
      </div>
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-16" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
  )
}
