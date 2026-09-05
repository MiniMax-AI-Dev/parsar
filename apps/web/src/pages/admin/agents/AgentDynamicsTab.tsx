import { useTranslation } from "react-i18next"

import {
  Ledger,
  LedgerHeader,
  LedgerId,
  LedgerNum,
  LedgerRow,
} from "../../../components/ui/ledger"
import { PropertyList, Property } from "../../../components/ui/property-list"
import { Skeleton } from "../../../components/ui/skeleton"
import { StatusIcon } from "../../../components/ui/status-icon"
import { useAdminView } from "../../../lib/admin-router"
import {
  useAgentMetrics,
  useAgentRuns,
  type AgentMetrics,
} from "../../../lib/api-agents"
import type { Agent, AgentRunSummary } from "../../../lib/api-types"
import { useRelativeTime } from "../../../lib/relative-time"
import { DetailSection } from "./DetailSection"

const RECENT_RUNS_LIMIT = 10

/** status icon · run id · conversation · duration · age */
const RUNS_COLUMNS = "14px 132px minmax(0,1fr) 64px 80px"

export function AgentDynamicsTab({ workspaceID, agent }: { workspaceID: string | null; agent: Agent }) {
  const { t } = useTranslation("admin")
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
    <>
      <DetailSection title={t("agents.detail.dynamics.current.title")} meta={inflight.length || undefined}>
        <RunsLedger
          runs={inflight}
          loading={inflightQ.isLoading}
          emptyLabel={t("agents.detail.dynamics.current.empty")}
        />
      </DetailSection>

      <DetailSection title={t("agents.detail.dynamics.metrics.title")}>
        <MetricsList metrics={metricsQ.data} loading={metricsQ.isLoading} />
      </DetailSection>

      <DetailSection title={t("agents.detail.dynamics.recent.title")} meta={recent.length || undefined}>
        <RunsLedger
          runs={recent}
          loading={recentQ.isLoading}
          emptyLabel={t("agents.detail.dynamics.recent.empty")}
        />
      </DetailSection>
    </>
  )
}

function RunsLedger({ runs, loading, emptyLabel }: { runs: AgentRunSummary[]; loading: boolean; emptyLabel: string }) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const fmtAgo = useRelativeTime()

  if (loading) {
    return (
      <div className="-mx-4">
        {Array.from({ length: 2 }).map((_, index) => (
          <div key={index} className="flex h-9 items-center gap-3 border-b border-line px-4">
            <Skeleton className="h-3.5 w-3.5 rounded-full" />
            <Skeleton className="h-3 w-32" />
            <Skeleton className="h-3 flex-1" />
            <Skeleton className="h-3 w-12" />
          </div>
        ))}
      </div>
    )
  }
  if (runs.length === 0) {
    return <p className="text-sm text-fg-muted">{emptyLabel}</p>
  }

  return (
    <Ledger columns={RUNS_COLUMNS} className="-mx-4 border-t border-line" role="listbox" aria-label={t("runs.table.run")}>
      <LedgerHeader>
        <span />
        <span>{t("runs.table.run")}</span>
        <span>{t("runs.table.conversation")}</span>
        <span className="text-right">{t("runs.table.duration")}</span>
        <span className="text-right">{t("runs.table.age")}</span>
      </LedgerHeader>
      <ul className="m-0 list-none p-0">
        {runs.map((run) => (
          <LedgerRow
            key={run.id}
            onClick={() => navigate("runs", { id: run.id })}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault()
                navigate("runs", { id: run.id })
              }
            }}
          >
            <StatusIcon status={run.status} title={t(`runStatus.${run.status}`)} />
            <LedgerId>{shortRunId(run.id)}</LedgerId>
            <LedgerId>{run.conversation_id || "—"}</LedgerId>
            <LedgerNum muted={!run.started_at || !run.finished_at}>
              {run.started_at && run.finished_at ? formatDurationMs(durationMs(run.started_at, run.finished_at)) : "—"}
            </LedgerNum>
            <span className="truncate text-right text-xs text-fg-muted">{fmtAgo(run.created_at)}</span>
          </LedgerRow>
        ))}
      </ul>
    </Ledger>
  )
}

function MetricsList({ metrics, loading }: { metrics?: AgentMetrics; loading: boolean }) {
  const { t } = useTranslation("admin")
  if (loading) {
    return (
      <div className="flex flex-col gap-2 pt-1">
        {Array.from({ length: 3 }).map((_, index) => <Skeleton key={index} className="h-3 w-56" />)}
      </div>
    )
  }
  if (!metrics || metrics.completed_count + metrics.failed_count === 0) {
    return <p className="text-sm text-fg-muted">{t("agents.detail.dynamics.metrics.empty")}</p>
  }
  return (
    <PropertyList className="max-w-2xl grid-cols-[140px_minmax(0,1fr)]">
      <Property label={t("agents.detail.dynamics.metrics.completed")} mono>
        {metrics.completed_count}
      </Property>
      <Property label={t("agents.detail.dynamics.metrics.successRate")} mono>
        {formatPercent(metrics.success_rate)}
      </Property>
      <Property label={t("agents.detail.dynamics.metrics.avgDuration")} mono>
        {formatDurationMs(metrics.avg_duration_ms)}
      </Property>
    </PropertyList>
  )
}

function shortRunId(id: string): string {
  return id.length <= 16 ? id : `${id.slice(0, 16)}…`
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
