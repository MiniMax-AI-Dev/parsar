import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import {
  AlertTriangle,
  ArrowUpRight,
  Bot,
  Check,
  CheckCircle2,
  Clock,
  Code,
  Database,
  FileText,
  KeyRound,
  ListFilter,
  Loader2,
  Play,
  Search,
  ShieldAlert,
  ShieldCheck,
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
import { Kbd } from "../../components/ui/kbd"
import { OffsetPagination } from "../../components/ui/offset-pagination"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon } from "../../components/ui/status-icon"
import { DetailRail, RailSection } from "../../components/ui/detail-rail"
import { PropertyList, Property } from "../../components/ui/property-list"
import {
  col,
  InitialTile,
  Ledger,
  LedgerGroup,
  LedgerHeader,
  LedgerId,
  LedgerNum,
  LedgerRow,
} from "../../components/ui/ledger"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
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
  useRequeueRun,
} from "../../lib/api-agents"
import type { AgentRunDetail, AgentRunEvent, AgentRunStatus, AgentRunSummary } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"
import { cn } from "../../lib/utils"

/* ------------------------------------------------------------------ */
/*  List page: the runs ledger + detail rail                            */
/* ------------------------------------------------------------------ */

const RUNS_PAGE_SIZE = 20

type RunFilter = "all" | "running" | "failed"

// "running" unions {running, queued} so a queued run waiting on the
// dispatcher still shows under "In flight".
const FILTER_STATUSES: Record<RunFilter, AgentRunStatus[]> = {
  all: [],
  running: ["running", "queued"],
  failed: ["failed"],
}

const FILTERS: RunFilter[] = ["all", "running", "failed"]

const GROUP_ORDER: AgentRunStatus[] = [
  "running",
  "queued",
  "failed",
  "interrupted",
  "completed",
  "cancelled",
]

/** status icon · run id · agent (+ failure reason) · conversation · connector · duration · age */
const LEDGER_COLUMNS = [col.icon(), col.id(132), col.title(), col.id(104, 0.5), col.meta(104), col.num(64), col.age(80)]

export function RunsPage({ selectedId }: { selectedId?: string | null }) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const wsId = useWorkspaceId()
  const fmtAgo = useRelativeTime()
  const [filter, setFilter] = useState<RunFilter>("all")
  const [keyword, setKeyword] = useState("")
  const [offset, setOffset] = useState(0)
  const searchRef = useRef<HTMLInputElement>(null)

  // Status filter is server-side (?status=a,b for the union case), so the
  // page always asks for exactly RUNS_PAGE_SIZE rows of the right kind.
  const statuses = FILTER_STATUSES[filter]
  const query = useAgentRuns(wsId, { statuses, offset, limit: RUNS_PAGE_SIZE })
  const runs = useMemo(() => query.data?.agent_runs ?? [], [query.data])
  const total = query.data?.total ?? 0

  // Offset is keyed by (filter, workspace): switching either starts from
  // page one so we never point past the end of the new result set.
  const [offsetKey, setOffsetKey] = useState(`${filter}:${wsId ?? ""}`)
  const currentKey = `${filter}:${wsId ?? ""}`
  if (offsetKey !== currentKey) {
    setOffsetKey(currentKey)
    setOffset(0)
  }

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  // Keyword search is client-side over the current page; backend has no
  // free-text index on agent_name / conversation_id.
  const filtered = useMemo(() => {
    if (!keyword) return runs
    const q = keyword.toLowerCase()
    return runs.filter((r) =>
      [r.agent_name ?? "", r.agent_slug ?? "", r.id, r.conversation_id ?? ""]
        .join(" ")
        .toLowerCase()
        .includes(q),
    )
  }, [runs, keyword])

  const groups = useMemo(
    () =>
      GROUP_ORDER.map((status) => ({
        status,
        runs: filtered.filter((r) => r.status === status),
      })).filter((g) => g.runs.length > 0),
    [filtered],
  )

  const select = (id: string | null) => {
    if (id) navigate("runs", { id })
    else navigate("runs")
  }

  // Keyboard: ⌘K focuses search; J/K (or arrows) move the selection.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        searchRef.current?.focus()
        return
      }
      const target = e.target as HTMLElement | null
      if (target?.closest("input, textarea, [contenteditable=true]")) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const forward = e.key === "j" || e.key === "ArrowDown"
      const backward = e.key === "k" || e.key === "ArrowUp"
      if (!forward && !backward) return
      const ordered = groups.flatMap((g) => g.runs)
      if (ordered.length === 0) return
      e.preventDefault()
      const i = ordered.findIndex((r) => r.id === selectedId)
      const next = forward
        ? ordered[Math.min(i + 1, ordered.length - 1)]
        : ordered[Math.max(i - 1, 0)]
      if (next && next.id !== selectedId) select(next.id)
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [groups, selectedId])

  const pageTitle = t("runs.page.title")
  const filterLabel = t("runs.filter.label")

  return (
    <AdminLayout activeMenu="runs" fullBleed>
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <PageHeader
            className="static mx-0 mb-0"
            title={pageTitle}
            subtitleFor="runs.page.title"
            action={
              <>
                <div className="relative w-72">
                  <Search
                    className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
                    strokeWidth={1.5}
                    aria-hidden="true"
                  />
                  <Input
                    ref={searchRef}
                    type="search"
                    placeholder={t("runs.search.placeholder")}
                    aria-label={t("runs.search.placeholder")}
                    className="pl-7 pr-11"
                    value={keyword}
                    onChange={(e) => setKeyword(e.target.value)}
                  />
                  <Kbd className="pointer-events-none absolute right-1.5 top-1/2 -translate-y-1/2">⌘K</Kbd>
                </div>
                <DropdownMenu.Root>
                  <DropdownMenu.Trigger asChild>
                    <Button variant="outline" aria-haspopup="menu">
                      <ListFilter strokeWidth={1.5} aria-hidden="true" />
                      {filterLabel}
                      {filter !== "all" && (
                        <span className="text-fg-muted">· {t(`runs.tabs.${filter}`)}</span>
                      )}
                    </Button>
                  </DropdownMenu.Trigger>
                  <DropdownMenu.Portal>
                    <DropdownMenu.Content
                      align="end"
                      sideOffset={6}
                      className="app-shadow-floating z-50 min-w-[180px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in data-[state=closed]:animate-pop-out"
                    >
                      <DropdownMenu.RadioGroup value={filter} onValueChange={(v) => setFilter(v as RunFilter)}>
                        {FILTERS.map((f) => (
                          <DropdownMenu.RadioItem
                            key={f}
                            value={f}
                            className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"
                          >
                            <span className="flex-1">{t(`runs.tabs.${f}`)}</span>
                            <DropdownMenu.ItemIndicator>
                              <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
                            </DropdownMenu.ItemIndicator>
                          </DropdownMenu.RadioItem>
                        ))}
                      </DropdownMenu.RadioGroup>
                    </DropdownMenu.Content>
                  </DropdownMenu.Portal>
                </DropdownMenu.Root>
              </>
            }
          />

          {!wsId ? (
            <div className="px-6"><ScopeRequiredState scope="workspace" resourceName={pageTitle} /></div>
          ) : query.isLoading ? (
            <RunsLoadingSkeleton />
          ) : err ? (
            <div className="px-6 pt-6">
              <ErrorState
                title={isUnreachable ? t("runs.loadError.unreachable.title") : t("runs.loadError.title")}
                description={
                  isUnreachable
                    ? t("runs.loadError.unreachable.description")
                    : err instanceof Error
                      ? err.message
                      : t("runs.loadError.description")
                }
                hint={isUnreachable ? t("runs.loadError.unreachable.hint") : t("runs.loadError.hint")}
                onRetry={() => void query.refetch()}
              />
            </div>
          ) : total === 0 ? (
            <EmptyState icon={Play} title={t("runs.empty.title")} description={t("runs.empty.description")} />
          ) : filtered.length === 0 ? (
            <EmptyState icon={Play} title={t("runs.emptyFiltered.title")} description={t("runs.emptyFiltered.description")} />
          ) : (
            <Ledger columns={LEDGER_COLUMNS} role="listbox" aria-label={pageTitle}>
              <LedgerHeader>
                <span />
                <span>{t("runs.table.run")}</span>
                <span>{t("runs.table.agent")}</span>
                <span>{t("runs.table.conversation")}</span>
                <span>{t("runs.table.connector")}</span>
                <span className="text-right">{t("runs.table.duration")}</span>
                <span className="text-right">{t("runs.table.age")}</span>
              </LedgerHeader>
              {groups.map((g) => (
                <LedgerGroup key={g.status} label={t(`runStatus.${g.status}`)} count={g.runs.length}>
                  {g.runs.map((r) => (
                    <RunRow
                      key={r.id}
                      run={r}
                      selected={r.id === selectedId}
                      statusLabel={t(`runStatus.${r.status}`)}
                      age={fmtAgo(r.created_at)}
                      onSelect={() => select(r.id)}
                    />
                  ))}
                </LedgerGroup>
              ))}
            </Ledger>
          )}

          {wsId && !query.isLoading && !err && (
            <OffsetPagination
              offset={offset}
              limit={RUNS_PAGE_SIZE}
              total={total}
              onPrevious={() => setOffset((cur) => Math.max(0, cur - RUNS_PAGE_SIZE))}
              onNext={() => setOffset((cur) => cur + RUNS_PAGE_SIZE)}
            />
          )}
        </div>

        {selectedId && wsId && (
          <RunDetailRail key={selectedId} id={selectedId} wsId={wsId} onClose={() => select(null)} />
        )}
      </div>
    </AdminLayout>
  )
}

function RunRow({
  run,
  selected,
  statusLabel,
  age,
  onSelect,
}: {
  run: AgentRunSummary
  selected: boolean
  statusLabel: string
  age: string
  onSelect: () => void
}) {
  const agent = run.agent_name ?? run.agent_slug ?? "—"
  const errorSummary = run.error_summary ?? run.user_facing_reason
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onSelect()
    }
  }
  return (
    <LedgerRow selected={selected} onClick={onSelect} onKeyDown={onKeyDown}>
      <StatusIcon status={run.status} title={statusLabel} />
      <LedgerId>{shortId(run.id, 16)}</LedgerId>
      <span className="flex min-w-0 items-center gap-1.5">
        <InitialTile name={agent} />
        <span className="shrink-0 truncate font-medium">{agent}</span>
        {errorSummary && (
          <span className="min-w-0 truncate text-xs text-fg-muted max-[1360px]:hidden">· {errorSummary}</span>
        )}
      </span>
      <LedgerId>{tailId(run.conversation_id)}</LedgerId>
      <span className="truncate text-xs text-fg-muted">{connectorLabel(run.connector_type)}</span>
      <LedgerNum muted={!run.started_at}>{fmtDuration(run.started_at, run.finished_at)}</LedgerNum>
      <span className="truncate text-right text-xs text-fg-muted">{age}</span>
    </LedgerRow>
  )
}

function RunsLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3.5 w-3.5 rounded-full" />
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Detail rail                                                        */
/* ------------------------------------------------------------------ */

function RunDetailRail({ id, wsId, onClose }: { id: string; wsId: string; onClose: () => void }) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const { navigate } = useAdminView()

  const runQ = useAgentRun(id, wsId)
  const cancelRun = useCancelRun(wsId)
  const requeueRun = useRequeueRun(wsId)
  const [confirmCancel, setConfirmCancel] = useState(false)
  const [cancelError, setCancelError] = useState<string | null>(null)

  const runData = runQ.data
  const eventsQ = useAgentRunEvents(runData?.id ?? null, wsId, { status: runData?.status, initialEvents: runData?.events })
  const events = eventsQ.data?.events ?? runData?.events ?? []

  const closeLabel = t("runs.detail.close")

  if (runQ.isLoading) {
    return (
      <DetailRail header={<Skeleton className="h-3 w-40" />} onClose={onClose} closeLabel={closeLabel}>
        <div className="space-y-3">
          <Skeleton className="h-4 w-48" />
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-3 w-full" />
          ))}
        </div>
      </DetailRail>
    )
  }

  if (runQ.error || !runData) {
    const err = runQ.error
    const isUnreachable = err instanceof ApiError && err.envelope.unreachable
    return (
      <DetailRail header={<LedgerId>{shortId(id, 12)}</LedgerId>} onClose={onClose} closeLabel={closeLabel}>
        <ErrorState
          title={isUnreachable ? t("runs.loadError.unreachable.title") : t("runs.loadError.title")}
          description={err instanceof Error ? err.message : t("runs.loadError.description")}
          hint={t("runs.loadError.hint")}
          onRetry={() => void runQ.refetch()}
        />
      </DetailRail>
    )
  }

  const run = runData
  const agent = run.agent_name ?? run.agent_slug ?? "—"
  const errorSummary = run.error_summary ?? run.user_facing_reason
  const translateDetail = (key: string, options?: Record<string, unknown>) => t(key as never, options as never) as unknown as string
  const diagnosis = buildRunDiagnosis(run, events, translateDetail)
  const runtimeDiagnosis = buildRuntimeDiagnosis(run, translateDetail)
  const isCancellable = run.status === "running" || run.status === "queued"
  const isRetryable = run.status === "failed" || run.status === "interrupted" || run.status === "cancelled"

  function handleRetry() {
    setCancelError(null)
    requeueRun.mutate(
      { runID: run.id, reason: "user_clicked_retry" },
      { onError: (e) => setCancelError(e instanceof Error ? e.message : String(e)) },
    )
  }

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

  return (
    <DetailRail
      aria-label={`${agent} · ${run.id}`}
      onClose={onClose}
      closeLabel={closeLabel}
      header={
        <>
          <StatusIcon status={run.status} />
          <span className="shrink-0 text-sm font-medium text-fg">{t(`runStatus.${run.status}`)}</span>
          <LedgerId className="min-w-0 flex-1">{run.id}</LedgerId>
        </>
      }
      footer={
        <>
          {isRetryable && (
            <Button variant="outline" onClick={handleRetry} disabled={requeueRun.isPending}>
              {requeueRun.isPending && <Loader2 className="animate-spin" />}
              {t("runs.actions.retry")}
            </Button>
          )}
          {isCancellable && (
            <Button variant="outline" onClick={() => setConfirmCancel(true)} disabled={cancelRun.isPending}>
              {cancelRun.isPending && <Loader2 className="animate-spin" />}
              {t("runs.actions.cancel.label")}
            </Button>
          )}
          {run.conversation_id && (
            <Button
              variant="link"
              className="ml-auto"
              onClick={() => navigate("conversations", { id: run.conversation_id! })}
            >
              {t("runs.detail.openConversation")}
              <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
            </Button>
          )}
        </>
      }
    >
      <h2 className="mb-3 flex items-center gap-2 text-sm font-medium text-fg">
        <InitialTile name={agent} />
        <span className="truncate">{agent}</span>
      </h2>

      {(errorSummary || cancelError) && (
        <p className="mb-3 flex items-start gap-1.5 break-words text-sm text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <span>{cancelError ?? errorSummary}</span>
        </p>
      )}

      <PropertyList>
        <Property label={t("runs.detail.connector")}>{connectorLabel(run.connector_type)}</Property>
        <Property label={t("runs.detail.conversation")} mono>
          {run.conversation_id ?? "—"}
        </Property>
        <Property label={t("runs.detail.created")} mono>{fmtDateTime(run.created_at)}</Property>
        <Property label={t("runs.detail.started")} mono>{fmtDateTime(run.started_at)}</Property>
        <Property label={t("runs.detail.finished")} mono>{fmtDateTime(run.finished_at)}</Property>
        <Property label={t("runs.detail.duration")} mono>{fmtDuration(run.started_at, run.finished_at)}</Property>
        {run.runtime && (
          <>
            <Property label={t("runs.detail.runtime.name")}>{run.runtime.name || shortId(run.runtime.id, 12)}</Property>
            <Property label={t("runs.detail.runtime.agentKind")}>{enumLabel(translateDetail, run.runtime.agent_kind)}</Property>
            <Property label={t("runs.detail.runtime.mode")}>{enumLabel(translateDetail, run.runtime.runtime_mode)}</Property>
            <Property label={t("runs.detail.runtime.model")} mono>{run.runtime.managed_model_id || "—"}</Property>
            <Property label={t("runs.detail.runtime.workdir")} mono>{run.runtime.working_directory || "—"}</Property>
            <Property label={t("runs.detail.runtime.lastHeartbeat")}>{runtimeDiagnosis.heartbeatAge}</Property>
          </>
        )}
      </PropertyList>

      <Tabs defaultValue="steps" className="mt-4">
        <TabsList className="flex w-full">
          <TabsTrigger value="overview" className="flex-1">{t("runs.detail.tabs.overview")}</TabsTrigger>
          <TabsTrigger value="steps" className="flex-1">{t("runs.detail.tabs.steps")}</TabsTrigger>
          <TabsTrigger value="artifacts" className="flex-1">{t("runs.detail.tabs.artifacts")}</TabsTrigger>
          <TabsTrigger value="audit" className="flex-1">{t("runs.detail.tabs.audit")}</TabsTrigger>
        </TabsList>

        <TabsContent value="overview">
          <RailSection title={t("runs.detail.overview.diagnostics")}>
            <PropertyList>
              <Property label={t("runs.detail.diagnostics.fields.result")}>
                <ToneBadge tone={diagnosis.tone} label={diagnosis.title} />
              </Property>
              <Property label={t("runs.detail.diagnostics.fields.reason")} className="h-auto min-h-7 whitespace-normal py-1 [overflow-wrap:anywhere]">
                {diagnosis.reason}
              </Property>
              <Property label={t("runs.detail.diagnostics.fields.source")}>{enumLabel(translateDetail, diagnosis.source)}</Property>
              <Property label={t("runs.detail.diagnostics.fields.nextAction")} className="h-auto min-h-7 whitespace-normal py-1">
                {diagnosis.action}
              </Property>
              <Property label={t("runs.detail.diagnostics.fields.latestEvent")}>{diagnosis.latest}</Property>
            </PropertyList>
          </RailSection>
          <RailSection title={t("runs.detail.overview.runtime")}>
            <PropertyList>
              <Property label={t("runs.detail.runtime.health")}>
                <ToneBadge tone={runtimeDiagnosis.tone} label={runtimeDiagnosis.health} />
              </Property>
              <Property label={t("runs.detail.runtime.action")} className="h-auto min-h-7 whitespace-normal py-1">
                {runtimeDiagnosis.action}
              </Property>
              {run.runtime ? (
                <>
                  <Property label={t("runs.detail.runtime.state")}>{enumLabel(translateDetail, run.runtime.liveness)}</Property>
                  <Property label={t("runs.detail.runtime.provider")}>{enumLabel(translateDetail, run.runtime.provider)}</Property>
                  <Property label={t("runs.detail.runtime.type")}>{enumLabel(translateDetail, run.runtime.type)}</Property>
                  <Property label={t("runs.detail.runtime.executionPlace")}>{enumLabel(translateDetail, run.runtime.execution_place)}</Property>
                  <Property label={t("runs.detail.runtime.governance")}>{enumLabel(translateDetail, run.runtime.governance_mode)}</Property>
                  <Property label={t("runs.detail.runtime.device")} mono>{run.runtime.device_id || "—"}</Property>
                  <Property label={t("runs.detail.runtime.sandbox")} mono>{run.runtime.sandbox_id || "—"}</Property>
                  <Property label={t("runs.detail.runtime.host")} mono>{run.runtime.hostname || "—"}</Property>
                  <Property label={t("runs.detail.runtime.version")} mono>{run.runtime.version || "—"}</Property>
                  <Property label={t("runs.detail.runtime.capturedAt")} mono>
                    {run.runtime.captured_at ? fmtDateTime(run.runtime.captured_at) : "—"}
                  </Property>
                  <Property label={t("runs.detail.runtime.capabilities")} className="h-auto min-h-7 flex-wrap py-1">
                    <RuntimeCapabilities capabilities={run.runtime.capabilities} />
                  </Property>
                </>
              ) : (
                <Property label={t("runs.detail.runtime.name")} className="text-fg-muted">
                  {t("runs.detail.runtime.empty")}
                </Property>
              )}
            </PropertyList>
          </RailSection>
        </TabsContent>

        <TabsContent value="steps">
          <RunSteps events={events} loading={eventsQ.isFetching && events.length === 0} />
        </TabsContent>

        <TabsContent value="artifacts">
          {run.artifacts && run.artifacts.length > 0 ? (
            <ul className="m-0 list-none p-0">
              {run.artifacts.map((a) => (
                <ArtifactRow key={a.id} medium={a.medium} kind={a.kind} name={a.name} meta={a.uri || undefined} />
              ))}
            </ul>
          ) : (
            <p className="py-6 text-center text-sm text-fg-muted">{tc("states.emptyTitle")}</p>
          )}
        </TabsContent>

        <TabsContent value="audit">
          <ResourceAuditTimeline wsId={wsId} targetType="agent_run" targetID={run.id} />
        </TabsContent>
      </Tabs>

      <RunCancelDialog
        open={confirmCancel}
        loading={cancelRun.isPending}
        onCancel={() => setConfirmCancel(false)}
        onConfirm={handleCancel}
      />
    </DetailRail>
  )
}

/* ------------------------------------------------------------------ */
/*  Steps                                                              */
/* ------------------------------------------------------------------ */

function RunSteps({ events, loading }: { events: AgentRunEvent[]; loading: boolean }) {
  const { t } = useTranslation("admin")
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(new Set())
  const steps = useMemo(() => {
    const translateStep = (key: string, options?: Record<string, unknown>) =>
      t(key as never, options as never) as unknown as string
    return buildSteps(events, translateStep)
  }, [events, t])
  const toggle = (key: string) => {
    setExpandedKeys((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  if (loading) {
    return (
      <div className="space-y-2 pt-2">
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-3/4" />
      </div>
    )
  }
  if (steps.length === 0) {
    return <p className="pt-2 text-sm text-fg-muted">{t("runs.detail.steps.empty")}</p>
  }

  return (
    <RailSection title={t("runs.detail.tabs.steps")} meta={steps.length}>
      <ol className="m-0 list-none p-0">
        {steps.map((step, index) => {
          const open = expandedKeys.has(step.key)
          const StepIcon = step.icon
          return (
            <li key={step.key} className="border-b border-line last:border-b-0">
              <div className="flex h-8 items-center gap-2 text-sm">
                <StepIcon className={cn("h-3.5 w-3.5 shrink-0", step.color)} strokeWidth={1.5} aria-hidden="true" />
                <span className="w-4 shrink-0 text-right font-mono text-xs tabular-nums text-fg-muted">{index + 1}</span>
                <span className="min-w-0 flex-1 truncate text-fg" title={step.detail ? `${step.title} · ${step.detail}` : step.title}>
                  {step.title}
                  {step.detail && <span className="text-fg-muted"> · {step.detail}</span>}
                </span>
                {step.rawEvents.length > 0 && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6"
                    aria-expanded={open}
                    aria-label={open ? t("runs.detail.steps.hideRaw") : t("runs.detail.steps.viewRaw")}
                    title={open ? t("runs.detail.steps.hideRaw") : t("runs.detail.steps.viewRaw")}
                    onClick={() => toggle(step.key)}
                  >
                    <Code className={cn(open && "text-fg")} strokeWidth={1.5} />
                  </Button>
                )}
              </div>
              {open && step.rawEvents.length > 0 && (
                <div className="space-y-2 pb-2">
                  {step.rawEvents.map((ev) => (
                    <pre key={ev.id} className="m-0 whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
                      {`#${ev.sequence} ${ev.event_kind}\n${JSON.stringify(ev.payload ?? {}, null, 2)}`}
                    </pre>
                  ))}
                </div>
              )}
            </li>
          )
        })}
      </ol>
    </RailSection>
  )
}

/* ------------------------------------------------------------------ */
/*  Helpers (formatters, diagnosis, step builders)                     */
/* ------------------------------------------------------------------ */

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

/** Product enum values (claude_code, sandbox, online…) in the UI's own words;
 *  unknown values fall back to the raw string with underscores as spaces. */
function enumLabel(t: AdminText, value?: string): string {
  if (!value) return "—"
  const key = `runs.enum.${value}`
  const translated = t(key)
  return translated === key ? value.replace(/_/g, " ") : translated
}

/** Distinguishing tail of a long id: the prefix is shared, the tail is not. */
function tailId(s?: string, n = 8): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(-n)
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
    case "permission.auto_denied":
      return t("runs.detail.steps.permissionAutoDenied")
    case "permission.auto_allowed":
      return t("runs.detail.steps.permissionAutoAllowed")
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

function RunCancelDialog({ open, loading, onCancel, onConfirm }: { open: boolean; loading: boolean; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{t("runs.actions.cancel.confirmTitle")}</DialogTitle>
        </DialogHeader>
        <DialogDescription className="flex items-start gap-2 text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <span>{t("runs.actions.cancel.confirmBody")}</span>
        </DialogDescription>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel} disabled={loading}>{tc("actions.cancel")}</Button>
          <Button variant="destructive" onClick={onConfirm} disabled={loading}>
            {loading && <Loader2 className="animate-spin" />}
            {t("runs.actions.cancel.label")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}


function ToneBadge({ tone, label }: { tone: DiagnosisTone; label: string }) {
  const status = tone === "success" ? "completed" : tone === "error" ? "failed" : tone === "warning" ? "interrupted" : "queued"
  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-fg">
      <StatusIcon status={status} />
      {label}
    </span>
  )
}


function RuntimeCapabilities({ capabilities }: { capabilities?: Record<string, boolean> }) {
  const entries = runtimeCapabilityEntries(capabilities)
  if (entries.length === 0) return <span>—</span>
  return (
    <span className="flex flex-wrap gap-1">
      {entries.map(([key, enabled]) => (
        <Badge key={key} variant={enabled ? "success" : "neutral"} dot>
          {key}
        </Badge>
      ))}
    </span>
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
      steps.push({ key: `delta-${deltaSequence}`, sequence: deltaSequence, title: t("runs.detail.steps.generated", { count: deltaCount }), occurredAt: deltaOccurredAt, icon: Bot, color: "text-fg-muted", rawEvents: deltaEvents })
      deltaCount = 0
      deltaOccurredAt = ""
      deltaEvents = []
    }
    const mapped = stepForEvent(ev, t)
    if (mapped) steps.push({ ...mapped, rawEvents: [ev] })
  }
  if (deltaCount > 0) {
    steps.push({ key: `delta-${deltaSequence}`, sequence: deltaSequence, title: t("runs.detail.steps.generated", { count: deltaCount }), occurredAt: deltaOccurredAt, icon: Bot, color: "text-fg-muted", rawEvents: deltaEvents })
  }
  return steps
}

function stepForEvent(ev: AgentRunEvent, t: RunStepT) {
  const withTime = { occurredAt: ev.occurred_at }
  switch (ev.event_kind) {
    case "message.complete":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.messageComplete"), detail: payloadValue(ev, "message_id"), icon: Bot, color: "text-fg-muted", ...withTime }
    case "tool.call":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.toolCall"), detail: payloadValue(ev, "name") || payloadValue(ev, "action") || "tool", icon: TerminalSquare, color: "text-fg-muted", ...withTime }
    case "tool.result":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.toolResult"), detail: payloadValue(ev, "name") || "tool", icon: Wrench, color: "text-fg-muted", ...withTime }
    case "permission.asked":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.permission"), detail: payloadValue(ev, "resource") || payloadValue(ev, "action") || "approval", icon: KeyRound, color: "text-status-running", ...withTime }
    case "permission.replied":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.permissionReplied"), detail: payloadValue(ev, "decision") || payloadValue(ev, "status"), icon: KeyRound, color: "text-status-completed", ...withTime }
    case "permission.auto_denied":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.permissionAutoDenied"), detail: payloadValue(ev, "hook_reason") || payloadValue(ev, "resource") || "denied by plugin", icon: ShieldAlert, color: "text-status-failed", ...withTime }
    case "permission.auto_allowed":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.permissionAutoAllowed"), detail: payloadValue(ev, "hook_reason") || payloadValue(ev, "resource") || "allowed by plugin", icon: ShieldCheck, color: "text-status-completed", ...withTime }
    case "model.changed":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.modelChanged"), detail: [payloadValue(ev, "from"), payloadValue(ev, "to")].filter(Boolean).join(" -> "), icon: Bot, color: "text-fg-muted", ...withTime }
    case "session.error":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.error"), detail: payloadValue(ev, "error"), icon: AlertTriangle, color: "text-status-failed", ...withTime }
    case "run.started":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.started"), detail: payloadValue(ev, "source"), icon: Play, color: "text-fg-muted", ...withTime }
    case "run.queued": {
      // run.queued payload may carry { position: N }; degrades to plain
      // "queued" when absent.
      const positionRaw = payloadValue(ev, "position")
      const position = positionRaw ? Number(positionRaw) : 0
      const detail = position > 1
        ? t("runs.detail.steps.queuedWithPosition", { position })
        : t("runs.detail.steps.queued")
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.queued"), detail, icon: Clock, color: "text-fg-muted", ...withTime }
    }
    case "run.completed":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.completed"), icon: CheckCircle2, color: "text-status-completed", ...withTime }
    case "run.failed":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.failed"), detail: payloadValue(ev, "error"), icon: XCircle, color: "text-status-failed", ...withTime }
    case "run.cancelled":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.cancelled"), detail: payloadValue(ev, "reason"), icon: XCircle, color: "text-fg-muted", ...withTime }
    case "run.requeued":
      return { key: ev.id, sequence: ev.sequence, title: t("runs.detail.steps.requeued"), detail: payloadValue(ev, "reason"), icon: Play, color: "text-status-running", ...withTime }
    default:
      return null
  }
}


// agent_run_artifacts splits into medium (where bytes live) and kind
// (what the artifact represents). Icon keys off kind.
function ArtifactRow({ medium, kind, name, meta }: { medium: string; kind: string; name: string; meta?: string }) {
  return (
    <li className="border-b border-line last:border-b-0">
      <button className="flex h-8 w-full items-center gap-2 text-left text-sm hover:app-hover">
        {kind === "diff" || kind === "patch" ? (
          <FileText className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        ) : (
          <Database className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        )}
        <code className="min-w-0 flex-1 truncate font-mono text-xs text-fg">{name}</code>
        {meta && <span className="truncate text-xs text-fg-muted">{kind} · {medium}</span>}
        <ArrowUpRight className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
      </button>
    </li>
  )
}
