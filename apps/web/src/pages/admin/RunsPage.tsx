import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  AlertTriangle,
  ArrowUpRight,
  Bot,
  CheckCircle2,
  Clock,
  Code,
  Database,
  FileText,
  KeyRound,
  Loader2,
  Play,
  Search,
  TerminalSquare,
  Wrench,
  XCircle,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ResourceAuditTimeline } from "../../components/admin/ResourceAuditTimeline"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { OffsetPagination } from "../../components/ui/offset-pagination"
import { Skeleton } from "../../components/ui/skeleton"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import {
  useCancelRun,
  useAgentRun,
  useAgentRunEvents,
  useAgentRuns,
} from "../../lib/api-agents"
import type { AgentRunDetail, AgentRunEvent, AgentRunStatus, AgentRunSummary } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"

/* ------------------------------------------------------------------ */
/*  Formatters                                                         */
/* ------------------------------------------------------------------ */

function fmtDuration(start?: string, end?: string): string {
  if (!start) return "—"
  const startMs = Date.parse(start)
  const endMs = end ? Date.parse(end) : Date.now()
  if (isNaN(startMs) || isNaN(endMs)) return "—"
  const sec = Math.max(0, Math.round((endMs - startMs) / 1000))
  if (sec < 60) return `0m ${String(sec).padStart(2, "0")}s`
  const m = Math.floor(sec / 60)
  const s = sec % 60
  return `${m}m ${String(s).padStart(2, "0")}s`
}

function connectorLabel(t: string): string {
  if (t === "agent_daemon") return "Agent Daemon"
  if (t === "http-agent" || t === "http") return "HTTP Agent"
  return t
}

function shortId(s?: string, n = 8): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(0, n) + "…"
}


type AdminText = (key: string, options?: Record<string, unknown>) => string
type DiagnosisTone = "success" | "warning" | "error" | "neutral"

interface RunDiagnosis {
  tone: DiagnosisTone
  title: string
  reason: string
  source: string
  action: string
  latest: string
}

interface RuntimeDiagnosis {
  tone: DiagnosisTone
  health: string
  heartbeatAge: string
  action: string
}

function fmtDateTime(value?: string): string {
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "—"
  return date.toLocaleString()
}

function runtimeCapabilityEntries(capabilities?: Record<string, boolean>): [string, boolean][] {
  return Object.entries(capabilities ?? {})
    .filter(([key]) => key.trim() !== "")
    .sort(([a], [b]) => a.localeCompare(b))
}

function fmtAge(value: string | undefined, t: AdminText): string {
  if (!value) return t("runs.detail.diagnostics.age.unknown")
  const ms = Date.parse(value)
  if (Number.isNaN(ms)) return t("runs.detail.diagnostics.age.unknown")
  const seconds = Math.max(0, Math.round((Date.now() - ms) / 1000))
  if (seconds < 60) return t("runs.detail.diagnostics.age.seconds", { count: seconds })
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return t("runs.detail.diagnostics.age.minutes", { count: minutes })
  const hours = Math.round(minutes / 60)
  if (hours < 48) return t("runs.detail.diagnostics.age.hours", { count: hours })
  return t("runs.detail.diagnostics.age.days", { count: Math.round(hours / 24) })
}

function valueString(value: unknown): string {
  if (typeof value === "string") return value.trim()
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  return ""
}

function metadataValue(metadata: Record<string, unknown> | undefined, key: string): string {
  return valueString(metadata?.[key])
}

function payloadValue(ev: AgentRunEvent | undefined, key: string): string {
  return valueString(ev?.payload?.[key])
}

function latestRunEvent(events: AgentRunEvent[]): AgentRunEvent | undefined {
  return events.reduce<AgentRunEvent | undefined>((latest, ev) => {
    if (!latest || ev.sequence > latest.sequence) return ev
    return latest
  }, undefined)
}

function lastEventOfKind(events: AgentRunEvent[], kinds: AgentRunEvent["event_kind"][]): AgentRunEvent | undefined {
  return events.reduce<AgentRunEvent | undefined>((latest, ev) => {
    if (!kinds.includes(ev.event_kind)) return latest
    if (!latest || ev.sequence > latest.sequence) return ev
    return latest
  }, undefined)
}

function eventTitle(ev: AgentRunEvent | undefined, t: AdminText): string {
  if (!ev) return t("runs.detail.diagnostics.noEvents")
  switch (ev.event_kind) {
    case "message.delta":
      return t("runs.detail.steps.generated", { count: String(ev.payload?.delta ?? "").length })
    case "message.complete":
      return t("runs.detail.steps.messageComplete")
    case "tool.call":
      return t("runs.detail.steps.toolCall")
    case "tool.result":
      return t("runs.detail.steps.toolResult")
    case "permission.asked":
      return t("runs.detail.steps.permission")
    case "permission.replied":
      return t("runs.detail.steps.permissionReplied")
    case "model.changed":
      return t("runs.detail.steps.modelChanged")
    case "session.error":
      return t("runs.detail.steps.error")
    case "run.started":
      return t("runs.detail.steps.started")
    case "run.completed":
      return t("runs.detail.steps.completed")
    case "run.failed":
      return t("runs.detail.steps.failed")
    case "run.cancelled":
      return t("runs.detail.steps.cancelled")
    case "run.requeued":
      return t("runs.detail.steps.requeued")
    default:
      return ev.event_kind
  }
}

function classifyFailure(reason: string): string {
  const lower = reason.toLowerCase()
  if (/not registered|not advertised|connector_type|agent_kind|connector/.test(lower)) return "connector"
  if (/credential|secret|token|unauthorized|permission denied|forbidden/.test(lower)) return "credential"
  if (/timeout|deadline|timed out|context deadline/.test(lower)) return "timeout"
  if (/offline|heartbeat|liveness|device|daemon|unavailable/.test(lower)) return "runtime"
  if (/model|provider|opencode_json|config/.test(lower)) return "model"
  return "inspect"
}

function hasRuntimeIssue(run: AgentRunDetail): boolean {
  const runtime = run.runtime
  if (!runtime) return run.connector_type === "agent_daemon" && ["queued", "running"].includes(run.status)
  const live = (runtime.liveness ?? "").toLowerCase()
  if (/disabled|offline|unhealthy|error|degraded/.test(live)) return true
  const heartbeatMs = runtime.last_heartbeat_at ? Date.parse(runtime.last_heartbeat_at) : NaN
  return run.status === "running" && Number.isFinite(heartbeatMs) && Date.now() - heartbeatMs > 120_000
}

function buildRunDiagnosis(run: AgentRunDetail, events: AgentRunEvent[], t: AdminText): RunDiagnosis {
  const latest = latestRunEvent(events)
  const failureEvent = lastEventOfKind(events, ["run.failed", "session.error"])
  const cancelEvent = lastEventOfKind(events, ["run.cancelled"])
  const requeueEvent = lastEventOfKind(events, ["run.requeued"])
  const cancelReason = metadataValue(run.metadata, "cancel_reason") || payloadValue(cancelEvent, "reason")
  const failureReason = run.error_summary
    || run.user_facing_reason
    || metadataValue(run.metadata, "failure_reason")
    || payloadValue(failureEvent, "error")
  const source = metadataValue(run.metadata, "failed_by")
    || metadataValue(run.metadata, "requeued_by")
    || payloadValue(failureEvent, "source")
    || run.connector_type
  const latestLabel = latest
    ? eventTitle(latest, t) + " · #" + latest.sequence
    : t("runs.detail.diagnostics.noEvents")

  if (run.status === "failed") {
    const reason = failureReason || t("runs.detail.diagnostics.reason.unknownFailure")
    return {
      tone: "error",
      title: t("runs.detail.diagnostics.status.failed"),
      reason,
      source: source || t("runs.detail.diagnostics.reason.unknownSource"),
      action: t("runs.detail.diagnostics.actions." + classifyFailure(reason)),
      latest: latestLabel,
    }
  }
  if (run.status === "cancelled") {
    return {
      tone: "neutral",
      title: t("runs.detail.diagnostics.status.cancelled"),
      reason: cancelReason || t("runs.detail.diagnostics.reason.cancelled"),
      source: payloadValue(cancelEvent, "source") || t("runs.detail.diagnostics.reason.user"),
      action: t("runs.detail.diagnostics.actions.requeueIfNeeded"),
      latest: latestLabel,
    }
  }
  if (run.status === "queued") {
    const requeueReason = metadataValue(run.metadata, "requeue_reason") || payloadValue(requeueEvent, "reason")
    return {
      tone: requeueReason ? "warning" : "neutral",
      title: requeueReason ? t("runs.detail.diagnostics.status.requeued") : t("runs.detail.diagnostics.status.queued"),
      reason: requeueReason || t("runs.detail.diagnostics.reason.waitingForRuntime"),
      source: metadataValue(run.metadata, "requeued_by") || payloadValue(requeueEvent, "source") || run.connector_type,
      action: t("runs.detail.diagnostics.actions.waitOrCancel"),
      latest: latestLabel,
    }
  }
  if (run.status === "running") {
    const runtimeIssue = hasRuntimeIssue(run)
    return {
      tone: runtimeIssue ? "warning" : "neutral",
      title: runtimeIssue ? t("runs.detail.diagnostics.status.runtimeDegraded") : t("runs.detail.diagnostics.status.running"),
      reason: runtimeIssue ? t("runs.detail.diagnostics.reason.runtimeNeedsAttention") : t("runs.detail.diagnostics.reason.running"),
      source: run.runtime?.name || run.runtime?.id || run.connector_type,
      action: runtimeIssue ? t("runs.detail.diagnostics.actions.inspectRuntime") : t("runs.detail.diagnostics.actions.watchEvents"),
      latest: latestLabel,
    }
  }
  if (run.status === "interrupted") {
    return {
      tone: "warning",
      title: t("runs.detail.diagnostics.status.interrupted"),
      reason: failureReason || t("runs.detail.diagnostics.reason.interrupted"),
      source: source || run.connector_type,
      action: t("runs.detail.diagnostics.actions.requeueIfNeeded"),
      latest: latestLabel,
    }
  }
  return {
    tone: "success",
    title: t("runs.detail.diagnostics.status.completed"),
    reason: t("runs.detail.diagnostics.reason.completed"),
    source: source || run.connector_type,
    action: t("runs.detail.diagnostics.actions.none"),
    latest: latestLabel,
  }
}

function buildRuntimeDiagnosis(run: AgentRunDetail, t: AdminText): RuntimeDiagnosis {
  const runtime = run.runtime
  if (!runtime) {
    const needsSnapshot = run.connector_type === "agent_daemon" && ["queued", "running", "failed"].includes(run.status)
    return {
      tone: needsSnapshot ? "warning" : "neutral",
      health: t("runs.detail.diagnostics.runtimeHealth.noSnapshot"),
      heartbeatAge: t("runs.detail.diagnostics.age.unknown"),
      action: t("runs.detail.diagnostics.runtimeActions.noSnapshot"),
    }
  }
  const live = (runtime.liveness ?? "").toLowerCase()
  const heartbeatMs = runtime.last_heartbeat_at ? Date.parse(runtime.last_heartbeat_at) : NaN
  const staleHeartbeat = run.status === "running" && Number.isFinite(heartbeatMs) && Date.now() - heartbeatMs > 120_000
  if (/offline|unhealthy|error|degraded/.test(live)) {
    return { tone: "error", health: t("runs.detail.diagnostics.runtimeHealth.offline"), heartbeatAge: fmtAge(runtime.last_heartbeat_at, t), action: t("runs.detail.diagnostics.runtimeActions.offline") }
  }
  if (staleHeartbeat) {
    return { tone: "warning", health: t("runs.detail.diagnostics.runtimeHealth.stale"), heartbeatAge: fmtAge(runtime.last_heartbeat_at, t), action: t("runs.detail.diagnostics.runtimeActions.stale") }
  }
  return { tone: "success", health: t("runs.detail.diagnostics.runtimeHealth.ready"), heartbeatAge: fmtAge(runtime.last_heartbeat_at, t), action: t("runs.detail.diagnostics.runtimeActions.ready") }
}

function toneBadgeVariant(tone: DiagnosisTone): "success" | "warning" | "destructive" | "neutral" {
  if (tone === "success") return "success"
  if (tone === "warning") return "warning"
  if (tone === "error") return "destructive"
  return "neutral"
}

/* ------------------------------------------------------------------ */
/*  Status badge                                                       */
/* ------------------------------------------------------------------ */

function RunStatusBadge({ status }: { status: AgentRunStatus }) {
  const { t } = useTranslation("admin")
  const labelKey = status === "queued"
    ? "queued"
    : status === "running"
      ? "running"
      : status === "completed"
        ? "completed"
        : status === "failed"
          ? "failed"
          : status === "cancelled"
            ? "cancelled"
            : "interrupted"
  switch (status) {
    case "queued": return <Badge variant="neutral">{t(`runStatus.${labelKey}`)}</Badge>
    case "running": return <Badge variant="primary" dot pulse>{t(`runStatus.${labelKey}`)}</Badge>
    case "completed": return <Badge variant="success" dot>{t(`runStatus.${labelKey}`)}</Badge>
    case "failed": return <Badge variant="destructive" dot>{t(`runStatus.${labelKey}`)}</Badge>
    case "cancelled": return <Badge variant="neutral">{t(`runStatus.${labelKey}`)}</Badge>
    case "interrupted": return <Badge variant="warning">{t(`runStatus.${labelKey}`)}</Badge>
  }
}

/* ------------------------------------------------------------------ */
/*  List page                                                          */
/* ------------------------------------------------------------------ */

const RUNS_PAGE_SIZE = 20

// "running" tab unions {running, queued} so a queued run waiting on the
// dispatcher still shows under "Running".
const TAB_STATUSES: Record<"all" | "running" | "failed", AgentRunStatus[]> = {
  all: [],
  running: ["running", "queued"],
  failed: ["failed"],
}

export function RunsPage() {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const wsId = useWorkspaceId()
  const [tab, setTab] = useState<"all" | "running" | "failed">("all")
  const [keyword, setKeyword] = useState("")
  const [offset, setOffset] = useState(0)

  // Status filter is now server-side (handler takes ?status=a,b for
  // the union case), so the page always asks for exactly RUNS_PAGE_SIZE
  // rows of the right kind — no over-fetch + client-side filter.
  const statuses = TAB_STATUSES[tab]
  const query = useAgentRuns(wsId, { statuses, offset, limit: RUNS_PAGE_SIZE })
  const runs = useMemo(() => query.data?.agent_runs ?? [], [query.data])
  const total = query.data?.total ?? 0

  // Reset offset on filter change so we don't point past the end of the
  // new result set.
  useEffect(() => {
    setOffset(0)
  }, [tab, wsId])

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  // Keyword search is client-side over the current page; backend has no
  // free-text index on agent_name / conversation_id.
  const filtered = runs.filter((r) => {
    if (!keyword) return true
    const q = keyword.toLowerCase()
    const haystack = [
      r.agent_name ?? "",
      r.agent_slug ?? "",
      r.id,
      r.conversation_id ?? "",
    ].join(" ").toLowerCase()
    return haystack.includes(q)
  })

  return (
    <AdminLayout activeMenu="runs">
      <PageHeader
        title={t("runs.page.title")}
        description={t("runs.page.description")}
      />
      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("runs.page.title")} />
      ) : query.isLoading ? (
        <RunsLoadingSkeleton />
      ) : err ? (
        <ErrorState
          title={
            isUnreachable
              ? t("runs.loadError.unreachable.title")
              : t("runs.loadError.title")
          }
          description={
            isUnreachable
              ? t("runs.loadError.unreachable.description")
              : err instanceof Error
                ? err.message
                : t("runs.loadError.description")
          }
          hint={
            isUnreachable
              ? t("runs.loadError.unreachable.hint")
              : t("runs.loadError.hint")
          }
          onRetry={() => void query.refetch()}
        />
      ) : (
        <div className="space-y-4">
          <div className="flex items-center justify-between gap-3">
            <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)}>
              <TabsList>
                <TabsTrigger value="all">{t("runs.tabs.all")}</TabsTrigger>
                <TabsTrigger value="running">{t("runs.tabs.running")}</TabsTrigger>
                <TabsTrigger value="failed">{t("runs.tabs.failed")}</TabsTrigger>
              </TabsList>
            </Tabs>
            <div className="relative w-72">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
              <Input
                placeholder={t("runs.search.placeholder")}
                className="pl-8 text-xs"
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
              />
            </div>
          </div>

          {total === 0 ? (
            // Empty state must live INSIDE the tabs container so an empty
            // "Running" / "Failed" tab doesn't take the tab bar with it — the user
            // has to be able to click "All" to get back.
            <EmptyState
              icon={Play}
              title={t("runs.empty.title")}
              description={t("runs.empty.description")}
            />
          ) : filtered.length === 0 ? (
            <EmptyState
              icon={Play}
              title={t("runs.emptyFiltered.title")}
              description={t("runs.emptyFiltered.description")}
            />
          ) : (
            <div className="overflow-hidden rounded-lg border border-line bg-surface">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("runs.table.run")}</TableHead>
                    <TableHead>{t("runs.table.status")}</TableHead>
                    <TableHead>{t("runs.table.conversation")}</TableHead>
                    <TableHead className="text-right">{t("runs.table.duration")}</TableHead>
                    <TableHead className="text-right pr-4">{t("runs.table.cost")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((r) => (
                    <RunRow
                      key={r.id}
                      run={r}
                      onClick={() => navigate("runs", { id: r.id })}
                    />
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          <OffsetPagination
            offset={offset}
            limit={RUNS_PAGE_SIZE}
            total={total}
            rangeLabel={({ from, to, total: rangeTotal }) =>
              t("runs.table.pagination.range", { from, to, total: rangeTotal })
            }
            previousLabel={t("runs.table.pagination.prev")}
            nextLabel={t("runs.table.pagination.next")}
            onPrevious={() => setOffset((cur) => Math.max(0, cur - RUNS_PAGE_SIZE))}
            onNext={() => setOffset((cur) => cur + RUNS_PAGE_SIZE)}
            className="text-sm text-fg-muted"
          />
        </div>
      )}
    </AdminLayout>
  )
}

function RunRow({ run, onClick }: { run: AgentRunSummary; onClick: () => void }) {
  const fmtAgo = useRelativeTime()
  const errorSummary = run.error_summary ?? run.user_facing_reason
  return (
    <TableRow className="cursor-pointer" onClick={onClick}>
      <TableCell>
        <div className="flex flex-col">
          <div className="flex items-center gap-2">
            <Bot className="h-3.5 w-3.5 shrink-0 text-fg-faint" strokeWidth={1.75} />
            <span className="text-base font-medium text-fg">{run.agent_name ?? run.agent_slug ?? "(unknown)"}</span>
          </div>
          <span className="font-mono text-xs text-fg-subtle">
            {shortId(run.id)} · {fmtAgo(run.created_at)}
          </span>
          {errorSummary && (
            <span className="mt-0.5 line-clamp-1 text-xs text-danger">{errorSummary}</span>
          )}
        </div>
      </TableCell>
      <TableCell><RunStatusBadge status={run.status} /></TableCell>
      <TableCell>
        <span className="font-mono text-xs text-fg-subtle">{shortId(run.conversation_id)}</span>
      </TableCell>
      <TableCell className="text-right text-sm tabular-nums text-fg-muted">
        {fmtDuration(run.started_at, run.finished_at)}
      </TableCell>
      <TableCell className="text-right pr-4 text-sm tabular-nums text-fg-faint">—</TableCell>
    </TableRow>
  )
}

function RunsLoadingSkeleton() {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-8 w-72" />
      </div>
      <div className="space-y-2 rounded-lg border border-line bg-surface p-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Run Detail                                                         */
/* ------------------------------------------------------------------ */

export function RunDetailPage({ id }: { id: string }) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const { navigate } = useAdminView()
  const wsId = useWorkspaceId()

  const runQ = useAgentRun(id, wsId)
  const cancelRun = useCancelRun(wsId)
  const [confirmCancel, setConfirmCancel] = useState(false)
  const [cancelError, setCancelError] = useState<string | null>(null)

  const runData = runQ.data
  const eventsQ = useAgentRunEvents(runData?.id ?? null, wsId, { status: runData?.status, initialEvents: runData?.events })
  const events = eventsQ.data?.events ?? runData?.events ?? []

  if (runQ.isLoading) {
    return (
      <AdminLayout activeMenu="runs">
        <RunsLoadingSkeleton />
      </AdminLayout>
    )
  }

  if (runQ.error || !runData) {
    const err = runQ.error
    const isUnreachable = err instanceof ApiError && err.envelope.unreachable
    return (
      <AdminLayout activeMenu="runs">
        <ErrorState
          title={
            isUnreachable
              ? t("runs.loadError.unreachable.title")
              : t("runs.loadError.title")
          }
          description={
            err instanceof Error ? err.message : t("runs.loadError.description")
          }
          hint={t("runs.loadError.hint")}
          onRetry={() => void runQ.refetch()}
        />
      </AdminLayout>
    )
  }

  const run = runData
  const errorSummary = run.error_summary ?? run.user_facing_reason
  const translateDetail = (key: string, options?: Record<string, unknown>) => t(key as never, options as never) as unknown as string
  const diagnosis = buildRunDiagnosis(run, events, translateDetail)
  const runtimeDiagnosis = buildRuntimeDiagnosis(run, translateDetail)

  function handleCancel() {
    setCancelError(null)
    cancelRun.mutate(
      { runID: run.id, reason: "user_clicked_cancel" },
      {
        onSuccess: () => setConfirmCancel(false),
        onError: (e) => setCancelError(e instanceof Error ? e.message : t("runs.actions.cancel.error")),
      }
    )
  }

  const isCancellable = run.status === "running" || run.status === "queued"

  return (
    <AdminLayout activeMenu="runs">
      <PageHeader
        backLink={
          <button
            onClick={() => navigate("runs")}
            className="hover:text-fg hover:underline"
          >
            ← {t("runs.page.title")}
          </button>
        }
        title={run.agent_name ?? run.agent_slug ?? "(unknown agent)"}
        description={
          <span className="font-mono text-sm">
            {shortId(run.id, 12)} · {connectorLabel(run.connector_type)}
          </span>
        }
        action={
          <>
            <RunStatusBadge status={run.status} />
            {isCancellable && (
              <Button size="sm" variant="outline" onClick={() => setConfirmCancel(true)} disabled={cancelRun.isPending || !wsId}>
                {cancelRun.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {t("runs.actions.cancel.label")}
              </Button>
            )}
          </>
        }
      />

      <div className="mb-4 grid grid-cols-2 gap-3 rounded-lg border border-line bg-surface p-4 md:grid-cols-4">
        <Field
          label={t("runs.detail.duration")}
          value={fmtDuration(run.started_at, run.finished_at)}
        />
        <Field label={t("runs.detail.cost")} value="—" mono />
        <Field
          label={t("runs.detail.conversation")}
          value={
            run.conversation_id ? (
              <button
                className="inline-flex items-center gap-1 hover:underline"
                onClick={() => navigate("conversations", { id: run.conversation_id! })}
              >
                <span className="font-mono text-sm">{shortId(run.conversation_id, 10)}</span>
                <ArrowUpRight className="h-3 w-3 text-fg-faint" />
              </button>
            ) : (
              "—"
            )
          }
        />
        <Field label={tc("nav.items.agents")} value={run.agent_name ?? "—"} />
      </div>

      {cancelError && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-danger-border bg-danger-subtle/40 p-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" strokeWidth={2} />
          <p className="font-mono text-sm text-danger-emphasis">{cancelError}</p>
        </div>
      )}

      {errorSummary && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-danger-border bg-danger-subtle/40 p-4">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" strokeWidth={2} />
          <div className="space-y-1">
            <p className="text-sm font-medium text-danger-emphasis">{t("runs.detail.errorTitle")}</p>
            <p className="text-sm text-danger-emphasis/85">{t("runs.detail.errorHint")}</p>
          </div>
        </div>
      )}

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">{t("runs.detail.tabs.overview")}</TabsTrigger>
          <TabsTrigger value="events">{t("runs.detail.tabs.steps")}</TabsTrigger>
          <TabsTrigger value="artifacts">{t("runs.detail.tabs.artifacts")}</TabsTrigger>
          <TabsTrigger value="audit">{t("runs.detail.tabs.audit")}</TabsTrigger>
        </TabsList>

        <TabsContent value="overview">
          <div className="space-y-4">
            <div className="grid gap-4 lg:grid-cols-2">
              <Card title={t("runs.detail.overview.lifecycle")}>
                <Field
                  label={t("runs.detail.diagnostics.fields.status")}
                  value={<RunStatusBadge status={run.status} />}
                />
                <Field label="run_id" value={run.id} mono />
                <Field label="created_at" value={fmtDateTime(run.created_at)} mono />
                <Field label="started_at" value={fmtDateTime(run.started_at)} mono />
                <Field label="finished_at" value={fmtDateTime(run.finished_at)} mono />
                <Field label={t("runs.detail.diagnostics.fields.latestEvent")} value={diagnosis.latest} mono />
              </Card>
              <Card title={t("runs.detail.overview.diagnostics")}>
                <Field
                  label={t("runs.detail.diagnostics.fields.result")}
                  value={<ToneBadge tone={diagnosis.tone} label={diagnosis.title} />}
                />
                <Field
                  label={t("runs.detail.diagnostics.fields.reason")}
                  value={<span className="break-words">{diagnosis.reason}</span>}
                  mono
                />
                <Field label={t("runs.detail.diagnostics.fields.source")} value={diagnosis.source || "—"} mono />
                <Field label={t("runs.detail.diagnostics.fields.nextAction")} value={diagnosis.action} />
              </Card>
            </div>
            <Card title={t("runs.detail.overview.runtime")}>
              {run.runtime ? (
                <>
                  <Field
                    label={t("runs.detail.runtime.health")}
                    value={<ToneBadge tone={runtimeDiagnosis.tone} label={runtimeDiagnosis.health} />}
                  />
                  <Field label={t("runs.detail.runtime.heartbeatAge")} value={runtimeDiagnosis.heartbeatAge} mono />
                  <Field label={t("runs.detail.runtime.action")} value={runtimeDiagnosis.action} />
                  <Field
                    label={t("runs.detail.runtime.name")}
                    value={
                      <div>
                        <span>{run.runtime.name || shortId(run.runtime.id, 12)}</span>
                        {run.runtime.id && run.runtime.name ? (
                          <div className="font-mono text-xs text-fg-subtle">{shortId(run.runtime.id, 12)}</div>
                        ) : null}
                      </div>
                    }
                  />
                  <Field
                    label={t("runs.detail.runtime.state")}
                    value={
                      run.runtime.liveness || "—"
                    }
                    mono
                  />
                  <Field label={t("runs.detail.runtime.provider")} value={run.runtime.provider || "—"} mono />
                  <Field label={t("runs.detail.runtime.type")} value={run.runtime.type || "—"} mono />
                  <Field label={t("runs.detail.runtime.connector")} value={run.runtime.connector_type || "—"} mono />
                  <Field label={t("runs.detail.runtime.agentKind")} value={run.runtime.agent_kind || "—"} mono />
                  <Field label={t("runs.detail.runtime.mode")} value={run.runtime.runtime_mode || "—"} mono />
                  <Field label={t("runs.detail.runtime.executionPlace")} value={run.runtime.execution_place || "—"} mono />
                  <Field label={t("runs.detail.runtime.governance")} value={run.runtime.governance_mode || "—"} mono />
                  <Field
                    label={t("runs.detail.runtime.device")}
                    value={<span className="break-all">{run.runtime.device_id || "—"}</span>}
                    mono
                  />
                  <Field
                    label={t("runs.detail.runtime.sandbox")}
                    value={<span className="break-all">{run.runtime.sandbox_id || "—"}</span>}
                    mono
                  />
                  <Field
                    label={t("runs.detail.runtime.model")}
                    value={<span className="break-all">{run.runtime.managed_model_id || "—"}</span>}
                    mono
                  />
                  <Field
                    label={t("runs.detail.runtime.workdir")}
                    value={<span className="break-all">{run.runtime.working_directory || "—"}</span>}
                    mono
                  />
                  <Field
                    label={t("runs.detail.runtime.capabilities")}
                    value={<RuntimeCapabilities capabilities={run.runtime.capabilities} />}
                  />
                  <Field label={t("runs.detail.runtime.host")} value={run.runtime.hostname || "—"} mono />
                  <Field label={t("runs.detail.runtime.version")} value={run.runtime.version || "—"} mono />
                  <Field
                    label={t("runs.detail.runtime.lastHeartbeat")}
                    value={run.runtime.last_heartbeat_at ? fmtDateTime(run.runtime.last_heartbeat_at) : "—"}
                    mono
                  />
                  <Field
                    label={t("runs.detail.runtime.capturedAt")}
                    value={run.runtime.captured_at ? fmtDateTime(run.runtime.captured_at) : "—"}
                    mono
                  />
                </>
              ) : (
                <>
                  <Field
                    label={t("runs.detail.runtime.health")}
                    value={<ToneBadge tone={runtimeDiagnosis.tone} label={runtimeDiagnosis.health} />}
                  />
                  <Field label={t("runs.detail.runtime.action")} value={runtimeDiagnosis.action} />
                  <p className="text-sm text-fg-subtle">{t("runs.detail.runtime.empty")}</p>
                </>
              )}
            </Card>
          </div>
        </TabsContent>

        <TabsContent value="events">
          <RunSteps events={events} loading={eventsQ.isFetching && events.length === 0} />
        </TabsContent>

        <TabsContent value="artifacts">
          <div className="overflow-hidden rounded-lg border border-line bg-surface">
            {run.artifacts && run.artifacts.length > 0 ? (
              run.artifacts.map((a) => (
                <ArtifactRow key={a.id} medium={a.medium} kind={a.kind} name={a.name} meta={a.uri || undefined} />
              ))
            ) : (
              <div className="p-6 text-center text-sm text-fg-subtle">No artifacts.</div>
            )}
          </div>
        </TabsContent>


        <TabsContent value="audit">
          <Card title={t("runs.detail.tabs.audit")}>
            <ResourceAuditTimeline wsId={wsId} targetType="agent_run" targetID={run.id} />
          </Card>
        </TabsContent>
      </Tabs>
      <RunCancelDialog
        open={confirmCancel}
        loading={cancelRun.isPending}
        onCancel={() => setConfirmCancel(false)}
        onConfirm={handleCancel}
      />
    </AdminLayout>
  )
}

function RunCancelDialog({ open, loading, onCancel, onConfirm }: { open: boolean; loading: boolean; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
        <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5 pr-5">
          <div className="shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis">
            <AlertTriangle className="h-4 w-4" />
          </div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">{t("runs.actions.cancel.confirmTitle")}</DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">
              {t("runs.actions.cancel.confirmBody")}
            </DialogDescription>
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
          <Button variant="outline" size="sm" onClick={onCancel} disabled={loading}>{tc("actions.cancel")}</Button>
          <Button variant="destructive" size="sm" onClick={onConfirm} disabled={loading}>
            {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("runs.actions.cancel.label")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ToneBadge({ tone, label }: { tone: DiagnosisTone; label: string }) {
  return <Badge variant={toneBadgeVariant(tone)}>{label}</Badge>
}

function RuntimeCapabilities({ capabilities }: { capabilities?: Record<string, boolean> }) {
  const entries = runtimeCapabilityEntries(capabilities)
  if (entries.length === 0) return <span>—</span>
  return (
    <div className="flex flex-wrap gap-1.5">
      {entries.map(([key, enabled]) => (
        <Badge key={key} variant={enabled ? "primary" : "neutral"}>
          {key}: {enabled ? "true" : "false"}
        </Badge>
      ))}
    </div>
  )
}

function Field({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="mb-2 last:mb-0">
      <dt className="mb-0.5 text-xs uppercase tracking-wider text-fg-faint">{label}</dt>
      <dd className={["min-w-0 text-sm text-fg-emphasis [overflow-wrap:anywhere]", mono ? "font-mono" : ""].filter(Boolean).join(" ")}>{value}</dd>
    </div>
  )
}

function Card({ title, actions, className, children }: { title: string; actions?: React.ReactNode; className?: string; children: React.ReactNode }) {
  return (
    <section className={`rounded-lg border border-line bg-surface p-4 ${className ?? ""}`}>
      <div className="mb-3 flex items-center justify-between gap-3">
        <h3 className="text-base font-semibold text-fg">{title}</h3>
        {actions}
      </div>
      {children}
    </section>
  )
}

function RunSteps({ events, loading }: { events: AgentRunEvent[]; loading: boolean }) {
  const { t } = useTranslation("admin")
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(new Set())
  const steps = useMemo(() => {
    const translateStep = (key: string, options?: Record<string, unknown>) =>
      t(key as never, options as never) as unknown as string
    return buildSteps(events, translateStep)
  }, [events, t])
  const expandable = useMemo(() => steps.filter((s) => s.rawEvents.length > 0), [steps])
  const allOpen = expandable.length > 0 && expandable.every((s) => expandedKeys.has(s.key))
  const toggle = (key: string) => {
    setExpandedKeys((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }
  const toggleAll = () => {
    setExpandedKeys(allOpen ? new Set() : new Set(expandable.map((s) => s.key)))
  }
  return (
    <Card
      title={t("runs.detail.steps.title")}
      actions={
        expandable.length > 0 && !loading ? (
          <button
            type="button"
            onClick={toggleAll}
            className="inline-flex items-center gap-1 rounded-md border border-line bg-surface px-2 py-1 text-xs font-medium text-fg-muted transition-colors hover:bg-surface-muted hover:text-fg"
          >
            <Code className="h-3.5 w-3.5" strokeWidth={1.9} />
            {allOpen ? t("runs.detail.steps.hideAllRaw") : t("runs.detail.steps.viewAllRaw")}
          </button>
        ) : null
      }
    >
      {loading ? (
        <div className="space-y-2">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-3/4" />
        </div>
      ) : steps.length === 0 ? (
        <p className="text-sm text-fg-subtle">{t("runs.detail.steps.empty")}</p>
      ) : (
        <ol className="space-y-3">
          {steps.map((step) => {
            const open = expandedKeys.has(step.key)
            return (
              <li key={step.key} className="rounded-lg border border-line-muted bg-surface-subtle/50 p-3">
                <div className="flex gap-3">
                  <step.icon className={`mt-0.5 h-4 w-4 shrink-0 ${step.color}`} strokeWidth={1.9} />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-3">
                      <p className="text-sm font-medium text-fg">{step.title}</p>
                      <div className="flex shrink-0 items-center gap-2">
                        <span className="whitespace-nowrap font-mono text-xs text-fg-faint">
                          {"#" + step.sequence + (step.occurredAt ? " · " + fmtDateTime(step.occurredAt) : "")}
                        </span>
                        {step.rawEvents.length > 0 && (
                          <button
                            type="button"
                            aria-expanded={open}
                            aria-label={open ? t("runs.detail.steps.hideRaw") : t("runs.detail.steps.viewRaw")}
                            title={open ? t("runs.detail.steps.hideRaw") : t("runs.detail.steps.viewRaw")}
                            onClick={() => toggle(step.key)}
                            className={`inline-flex items-center rounded-md border p-1 transition-colors ${
                              open
                                ? "border-line-strong bg-surface-muted text-fg-muted"
                                : "border-line bg-surface text-fg-subtle hover:border-line-strong hover:bg-surface-muted hover:text-fg-emphasis"
                            }`}
                          >
                            <Code className="h-3 w-3" strokeWidth={2} />
                          </button>
                        )}
                      </div>
                    </div>
                    {step.detail && <p className="mt-1 break-words text-sm text-fg-subtle">{step.detail}</p>}
                  </div>
                </div>
                {open && step.rawEvents.length > 0 && (
                  <div className="mt-2 space-y-2">
                    {step.rawEvents.map((ev) => (
                      <pre key={ev.id} className="whitespace-pre-wrap break-all rounded-md bg-surface-inverse p-3 text-xs leading-relaxed text-fg-on-emphasis">
                        {`#${ev.sequence} ${ev.event_kind}\n${JSON.stringify(ev.payload ?? {}, null, 2)}`}
                      </pre>
                    ))}
                  </div>
                )}
              </li>
            )
          })}
        </ol>
      )}
    </Card>
  )
}

type RunStepT = (key: string, options?: Record<string, unknown>) => string

type BuiltStep = {
  key: string
  sequence: number
  title: string
  detail?: string
  occurredAt?: string
  icon: typeof Bot
  color: string
  rawEvents: AgentRunEvent[]
}

function buildSteps(events: AgentRunEvent[], t: RunStepT): BuiltStep[] {
  const steps: BuiltStep[] = []
  let deltaCount = 0
  let deltaSequence = 0
  let deltaOccurredAt = ""
  let deltaEvents: AgentRunEvent[] = []
  for (const ev of events) {
    if (ev.event_kind === "message.delta") {
      deltaCount += String(ev.payload?.delta ?? "").length
      deltaSequence = ev.sequence
      deltaOccurredAt = ev.occurred_at
      deltaEvents.push(ev)
      continue
    }
    if (deltaCount > 0) {
      steps.push({ key: `delta-${deltaSequence}`, sequence: deltaSequence, title: t("runs.detail.steps.generated", { count: deltaCount }), occurredAt: deltaOccurredAt, icon: Bot, color: "text-info", rawEvents: deltaEvents })
      deltaCount = 0
      deltaOccurredAt = ""
      deltaEvents = []
    }
    const mapped = stepForEvent(ev, t)
    if (mapped) steps.push({ ...mapped, rawEvents: [ev] })
  }
  if (deltaCount > 0) {
    steps.push({ key: `delta-${deltaSequence}`, sequence: deltaSequence, title: t("runs.detail.steps.generated", { count: deltaCount }), occurredAt: deltaOccurredAt, icon: Bot, color: "text-info", rawEvents: deltaEvents })
  }
  return steps
}

function stepForEvent(ev: AgentRunEvent, t: RunStepT) {
  const withTime = { occurredAt: ev.occurred_at }
  switch (ev.event_kind) {
    case "message.complete":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.messageComplete"), detail: payloadValue(ev, "message_id"), icon: Bot, color: "text-info", ...withTime }
    case "tool.call":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.toolCall"), detail: payloadValue(ev, "name") || payloadValue(ev, "action") || "tool", icon: TerminalSquare, color: "text-fg-muted", ...withTime }
    case "tool.result":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.toolResult"), detail: payloadValue(ev, "name") || "tool", icon: Wrench, color: "text-fg-muted", ...withTime }
    case "permission.asked":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.permission"), detail: payloadValue(ev, "resource") || payloadValue(ev, "action") || "approval", icon: KeyRound, color: "text-warning", ...withTime }
    case "permission.replied":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.permissionReplied"), detail: payloadValue(ev, "decision") || payloadValue(ev, "status"), icon: KeyRound, color: "text-success", ...withTime }
    case "model.changed":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.modelChanged"), detail: [payloadValue(ev, "from"), payloadValue(ev, "to")].filter(Boolean).join(" -> "), icon: Bot, color: "text-info", ...withTime }
    case "session.error":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.error"), detail: payloadValue(ev, "error"), icon: AlertTriangle, color: "text-danger", ...withTime }
    case "run.started":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.started"), detail: payloadValue(ev, "source"), icon: Play, color: "text-info", ...withTime }
    case "run.queued": {
      // run.queued payload may carry { position: N }; degrades to plain
      // "queued" when absent.
      const positionRaw = payloadValue(ev, "position")
      const position = positionRaw ? Number(positionRaw) : 0
      const detail = position > 1
        ? t("runs.detail.steps.queuedWithPosition", { position })
        : t("runs.detail.steps.queued")
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.queued"), detail, icon: Clock, color: "text-fg-subtle", ...withTime }
    }
    case "run.completed":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.completed"), icon: CheckCircle2, color: "text-success", ...withTime }
    case "run.failed":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.failed"), detail: payloadValue(ev, "error"), icon: XCircle, color: "text-danger", ...withTime }
    case "run.cancelled":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.cancelled"), detail: payloadValue(ev, "reason"), icon: XCircle, color: "text-fg-subtle", ...withTime }
    case "run.requeued":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.requeued"), detail: payloadValue(ev, "reason"), icon: Play, color: "text-warning", ...withTime }
    default:
      return null
  }
}

// agent_run_artifacts splits into medium (where bytes live) and kind
// (what the artifact represents). Icon keys off kind.
function ArtifactRow({ medium, kind, name, meta }: { medium: string; kind: string; name: string; meta?: string }) {
  return (
    <button className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-surface-subtle/60">
      {kind === "diff" || kind === "patch" ? (
        <FileText className="h-4 w-4 text-fg-faint" strokeWidth={1.75} />
      ) : (
        <Database className="h-4 w-4 text-fg-faint" strokeWidth={1.75} />
      )}
      <div className="flex-1">
        <code className="text-sm font-medium text-fg">{name}</code>
        {meta && <p className="text-xs text-fg-subtle">{kind} · {medium} · {meta}</p>}
      </div>
      <ArrowUpRight className="h-3 w-3 text-fg-faint" strokeWidth={1.75} />
    </button>
  )
}
