import { ArrowUpRight } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Skeleton } from "../../../components/ui/skeleton"
import { useAdminView } from "../../../lib/admin-router"
import {
  useAgentMetrics,
  useAgentRuns,
  type AgentMetrics,
} from "../../../lib/api-agents"
import type { Agent, AgentRunStatus, AgentRunSummary } from "../../../lib/api-types"
import { useRelativeTime } from "../../../lib/relative-time"

const RECENT_RUNS_LIMIT = 10

export function AgentDynamicsTab({ workspaceID, agent }: { workspaceID: string | null; agent: Agent }) {
  const inflightQ = useAgentRuns(workspaceID, { statuses: ["running", "queued"], limit: 50 })
  const recentQ = useAgentRuns(workspaceID, { limit: 50 })
  const metricsQ = useAgentMetrics(workspaceID, agent.id, 30)

  const inflight = (inflightQ.data?.agent_runs ?? []).filter(
    (run) => run.agent_id === agent.id,
  )
  const recent = (recentQ.data?.agent_runs ?? [])
    .filter((run) => run.agent_id === agent.id)
    .slice(0, RECENT_RUNS_LIMIT)

  return (
    <div className="space-y-4">
      <CurrentWorkCard
        runs={inflight}
        loading={inflightQ.isLoading}
      />
      <MetricsCard
        metrics={metricsQ.data}
        loading={metricsQ.isLoading}
      />
      <RecentRunsCard
        runs={recent}
        loading={recentQ.isLoading}
        showCount={recent.length}
      />
    </div>
  )
}

function CurrentWorkCard({ runs, loading }: { runs: AgentRunSummary[]; loading: boolean }) {
  const { t } = useTranslation("admin")
  return (
    <DynamicsCard
      title={t("agents.detail.dynamics.current.title")}
      subtitle={t("agents.detail.dynamics.current.subtitle")}
    >
      {loading ? (
        <Skeleton className="h-5 w-2/3" />
      ) : runs.length === 0 ? (
        <p className="text-sm text-fg-faint">{t("agents.detail.dynamics.current.empty")}</p>
      ) : (
        <ul className="space-y-2">
          {runs.map((run) => (
            <li key={run.id} className="flex items-center justify-between rounded-md border border-line px-3 py-2">
              <div className="flex items-center gap-2 text-sm">
                <RunStatusDot status={run.status} />
                <code className="font-mono text-sm text-fg-muted">{shortRunId(run.id)}</code>
                <span className="text-fg-subtle">·</span>
                <span className="text-fg-muted">{run.agent_name ?? "—"}</span>
              </div>
              <span className="text-sm text-fg-subtle">{run.status}</span>
            </li>
          ))}
        </ul>
      )}
    </DynamicsCard>
  )
}

function MetricsCard({ metrics, loading }: { metrics?: AgentMetrics; loading: boolean }) {
  const { t } = useTranslation("admin")
  return (
    <DynamicsCard
      title={t("agents.detail.dynamics.metrics.title")}
      subtitle={t("agents.detail.dynamics.metrics.subtitle")}
    >
      {loading ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          {Array.from({ length: 3 }).map((_, index) => <Skeleton key={index} className="h-16 w-full" />)}
        </div>
      ) : !metrics || metrics.completed_count + metrics.failed_count === 0 ? (
        <p className="text-sm text-fg-faint">{t("agents.detail.dynamics.metrics.empty")}</p>
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <MetricStat
            label={t("agents.detail.dynamics.metrics.completed")}
            value={metrics.completed_count.toString()}
          />
          <MetricStat
            label={t("agents.detail.dynamics.metrics.successRate")}
            value={formatPercent(metrics.success_rate)}
          />
          <MetricStat
            label={t("agents.detail.dynamics.metrics.avgDuration")}
            value={formatDurationMs(metrics.avg_duration_ms)}
          />
        </div>
      )}
    </DynamicsCard>
  )
}

function MetricStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-line bg-surface-subtle/40 px-3 py-2">
      <div className="text-xs font-medium text-fg-faint">{label}</div>
      <div className="mt-0.5 text-2xl font-semibold tabular-nums text-fg">{value}</div>
    </div>
  )
}

function RecentRunsCard({
  runs,
  loading,
  showCount,
}: {
  runs: AgentRunSummary[]
  loading: boolean
  showCount: number
}) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const fmtAgo = useRelativeTime()
  return (
    <DynamicsCard
      title={t("agents.detail.dynamics.recent.title")}
      subtitle={t("agents.detail.dynamics.recent.subtitle", { count: showCount })}
    >
      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 3 }).map((_, index) => <Skeleton key={index} className="h-10 w-full" />)}
        </div>
      ) : runs.length === 0 ? (
        <p className="text-sm text-fg-faint">{t("agents.detail.dynamics.recent.empty")}</p>
      ) : (
        <ul className="space-y-2">
          {runs.map((run) => (
            <li key={run.id}>
              <button
                type="button"
                onClick={() => navigate("runs", { id: run.id })}
                className="flex w-full items-center gap-3 rounded-md border border-line px-3 py-2 text-left hover:bg-surface-subtle"
              >
                <RunStatusDot status={run.status} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 text-sm">
                    <code className="font-mono text-sm text-fg-muted">{shortRunId(run.id)}</code>
                    <span className="truncate text-fg-muted">{run.agent_name ?? t("agents.detail.dynamics.recent.untitled")}</span>
                  </div>
                  <div className="mt-0.5 text-xs text-fg-subtle">
                    {fmtAgo(run.created_at)}
                    {run.started_at && run.finished_at && (
                      <> · {formatDurationMs(durationMs(run.started_at, run.finished_at))}</>
                    )}
                  </div>
                </div>
                <ArrowUpRight className="h-3 w-3 text-fg-faint" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </DynamicsCard>
  )
}

function DynamicsCard({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle?: string
  children: React.ReactNode
}) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <div className="mb-3 flex items-baseline gap-2">
        <h3 className="text-base font-semibold text-fg">{title}</h3>
        {subtitle && <span className="text-sm text-fg-subtle">{subtitle}</span>}
      </div>
      {children}
    </section>
  )
}

function RunStatusDot({ status }: { status: AgentRunStatus }) {
  const tone =
    status === "completed" ? "bg-success"
      : status === "running" || status === "queued" ? "bg-info"
      : status === "failed" ? "bg-danger"
      : "bg-surface-muted"
  return <span className={`h-2 w-2 shrink-0 rounded-full ${tone}`} />
}

function shortRunId(id: string): string {
  return id.length <= 8 ? id : id.slice(0, 8)
}

function durationMs(startISO: string, endISO: string): number {
  return Math.max(0, Date.parse(endISO) - Date.parse(startISO))
}

function formatPercent(rate: number): string {
  return `${(rate * 100).toFixed(rate >= 0.995 ? 0 : 1)}%`
}

function formatDurationMs(ms: number): string {
  if (!ms || ms <= 0) return "—"
  if (ms < 1000) return `${Math.round(ms)}ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`
  const minutes = Math.floor(seconds / 60)
  const remainder = Math.round(seconds - minutes * 60)
  return remainder === 0 ? `${minutes}m` : `${minutes}m${remainder}s`
}
