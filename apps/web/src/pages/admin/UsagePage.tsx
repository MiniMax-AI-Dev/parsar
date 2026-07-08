import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { LineChart as LineChartIcon } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { Badge } from "../../components/ui/badge"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
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
import { useUsage } from "../../lib/api-governance"
import type { UsageLog } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"

/* ------------------------------------------------------------------ */
/*  Aggregation                                                        */
/* ------------------------------------------------------------------ */

interface UsageSummary {
  runs: Set<string>
  inputTokens: number
  outputTokens: number
  costUsd: number
}

function summarize(logs: UsageLog[]): UsageSummary {
  const summary: UsageSummary = {
    runs: new Set(),
    inputTokens: 0,
    outputTokens: 0,
    costUsd: 0,
  }
  for (const u of logs) {
    if (u.agent_run_id) summary.runs.add(u.agent_run_id)
    summary.inputTokens += u.input_tokens ?? 0
    summary.outputTokens += u.output_tokens ?? 0
    summary.costUsd += u.cost_usd ?? 0
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
        callCount: 0,
      }
      map.set(key, row)
    }
    row.inputTokens += u.input_tokens ?? 0
    row.outputTokens += u.output_tokens ?? 0
    row.costUsd += u.cost_usd ?? 0
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

function fmtInt(n: number): string {
  return n.toLocaleString()
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, { hour12: false })
}

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

  return (
    <AdminLayout activeMenu="settings">
      <PageHeader
        title={t("usage.page.title")}
      />
      <SettingsTabs active="usage" />
      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("usage.page.title")} />
      ) : query.isLoading ? (
        <UsageLoadingSkeleton />
      ) : err ? (
        <ErrorState
          title={isUnreachable ? t("usage.loadError.unreachable.title") : t("usage.loadError.title")}
          description={
            isUnreachable
              ? t("usage.loadError.unreachable.description")
              : err instanceof Error
                ? err.message
                : t("usage.loadError.description")
          }
          hint={isUnreachable ? t("usage.loadError.unreachable.hint") : t("usage.loadError.hint")}
          onRetry={() => void query.refetch()}
        />
      ) : logs.length === 0 ? (
        <EmptyState
          icon={LineChartIcon}
          title={t("usage.empty.title")}
          description={t("usage.empty.description")}
        />
      ) : (
        <div className="space-y-6">
          {/* Summary stats */}
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Stat label={t("usage.stats.runs")} value={String(summary.runs.size)} />
            <Stat label={t("usage.stats.inputTokens")} value={fmtInt(summary.inputTokens)} />
            <Stat label={t("usage.stats.outputTokens")} value={fmtInt(summary.outputTokens)} />
            <Stat label={t("usage.stats.cost")} value={fmtUsd(summary.costUsd)} mono />
          </div>

          {/* By model */}
          <section>
            <h2 className="mb-3 text-sm font-semibold text-fg">{t("usage.byModel.title")}</h2>
            <div className="overflow-hidden rounded-lg border border-line bg-surface">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("usage.byModel.provider")}</TableHead>
                    <TableHead>{t("usage.byModel.model")}</TableHead>
                    <TableHead className="text-right">{t("usage.byModel.calls")}</TableHead>
                    <TableHead className="text-right">{t("usage.byModel.input")}</TableHead>
                    <TableHead className="text-right">{t("usage.byModel.output")}</TableHead>
                    <TableHead className="text-right pr-4">{t("usage.byModel.cost")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {byModel.map((m) => (
                    <TableRow key={m.key}>
                      <TableCell className="text-sm text-fg-muted">{m.provider}</TableCell>
                      <TableCell><code className="text-sm text-fg-emphasis">{m.model}</code></TableCell>
                      <TableCell className="text-right text-sm tabular-nums text-fg-muted">{m.callCount}</TableCell>
                      <TableCell className="text-right text-sm tabular-nums text-fg-muted">{fmtInt(m.inputTokens)}</TableCell>
                      <TableCell className="text-right text-sm tabular-nums text-fg-muted">{fmtInt(m.outputTokens)}</TableCell>
                      <TableCell className="text-right pr-4 font-mono text-sm tabular-nums text-fg-muted">{fmtUsd(m.costUsd)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </section>

          {/* Recent calls */}
          <section>
            <h2 className="mb-3 text-sm font-semibold text-fg">{t("usage.recent.title")}</h2>
            <div className="overflow-hidden rounded-lg border border-line bg-surface">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("usage.recent.time")}</TableHead>
                    <TableHead>{t("usage.recent.run")}</TableHead>
                    <TableHead>{t("usage.recent.provider")}</TableHead>
                    <TableHead>{t("usage.recent.model")}</TableHead>
                    <TableHead className="text-right">{t("usage.recent.input")}</TableHead>
                    <TableHead className="text-right">{t("usage.recent.output")}</TableHead>
                    <TableHead className="text-right pr-4">{t("usage.recent.cost")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {logs.map((u) => (
                    <TableRow key={u.id}>
                      <TableCell className="font-mono text-xs text-fg-subtle tabular-nums">{fmtTime(u.created_at)}</TableCell>
                      <TableCell>
                        {u.agent_run_id ? (
                          <button
                            className="font-mono text-xs text-fg-muted hover:underline"
                            onClick={() => navigate("runs", { id: u.agent_run_id! })}
                          >
                            {shortId(u.agent_run_id)}
                          </button>
                        ) : (
                          <Badge variant="neutral">{t("usage.recent.noRun")}</Badge>
                        )}
                      </TableCell>
                      <TableCell className="text-sm text-fg-muted">{u.provider}</TableCell>
                      <TableCell><code className="text-sm text-fg-emphasis">{u.model}</code></TableCell>
                      <TableCell className="text-right text-sm tabular-nums text-fg-muted">{fmtInt(u.input_tokens)}</TableCell>
                      <TableCell className="text-right text-sm tabular-nums text-fg-muted">{fmtInt(u.output_tokens)}</TableCell>
                      <TableCell className="text-right pr-4 font-mono text-sm tabular-nums text-fg-muted">{fmtUsd(u.cost_usd)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </section>
        </div>
      )}
    </AdminLayout>
  )
}

function Stat({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="text-xs font-medium text-fg-faint">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums text-fg ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  )
}

function UsageLoadingSkeleton() {
  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-20 w-full" />
        ))}
      </div>
      <Skeleton className="h-40 w-full" />
      <Skeleton className="h-64 w-full" />
    </div>
  )
}
