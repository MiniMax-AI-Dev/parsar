import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { AlertTriangle, CalendarClock, Check, Loader2, Pencil, Play, Plus, Power, Trash2 } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ActionIconButton, RowActions } from "../../components/ui/action-button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../components/ui/alert-dialog"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Field } from "../../components/ui/label"
import {
  InitialTile,
  Ledger,
  LedgerHeader,
  LedgerId,
  LedgerNum,
  LedgerRow,
} from "../../components/ui/ledger"
import { OffsetPagination } from "../../components/ui/offset-pagination"
import { Property, PropertyList } from "../../components/ui/property-list"
import { Select } from "../../components/ui/select"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon, type StatusKind } from "../../components/ui/status-icon"
import { Textarea } from "../../components/ui/textarea"
import { ApiError } from "../../lib/api-client"
import { useAgents } from "../../lib/api-agents"
import { useWorkspaceId } from "../../lib/workspace"
import type { Agent } from "../../lib/api-types"
import {
  useCreateScheduledTask,
  useDeleteScheduledTask,
  useRunScheduledTaskNow,
  useScheduledTasksByWorkspace,
  useUpdateScheduledTask,
  type ScheduledTask,
} from "../../lib/api-scheduled-tasks"

type FreqType = "hourly" | "daily" | "weekly" | "monthly" | "weekday" | "custom"

const SCHED_PAGE_SIZE = 20

/** status icon · name · schedule · cron · agent · next run · last run · actions */
const LEDGER_COLUMNS = "14px minmax(0,1.2fr) minmax(0,1fr) 104px 140px 120px 120px 128px"

const FALLBACK_TZS = [
  "UTC",
  "Asia/Shanghai",
  "Asia/Hong_Kong",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Asia/Kolkata",
  "Europe/London",
  "Europe/Paris",
  "America/New_York",
  "America/Los_Angeles",
]

// Prefer the runtime's full IANA list; fall back to a short common set when
// Intl.supportedValuesOf is unavailable. The current value is always kept so an
// unusual stored timezone stays selectable.
function timezoneOptions(current: string): string[] {
  let zones: string[]
  try {
    const supported = (Intl as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf
    zones = supported ? supported("timeZone") : [...FALLBACK_TZS]
  } catch {
    zones = [...FALLBACK_TZS]
  }
  if (current && !zones.includes(current)) zones = [current, ...zones]
  return zones
}

function pad(n: number): string {
  return String(n).padStart(2, "0")
}

function fmtTime(h: number, m: number): string {
  return `${pad(h)}:${pad(m)}`
}

function buildCron(
  freq: FreqType,
  timeStr: string,
  dow: number,
  dom: number,
  minute: number,
  custom: string,
): string {
  const [hh, mm] = timeStr.split(":").map((v) => Number(v))
  switch (freq) {
    case "hourly":
      return `${minute} * * * *`
    case "daily":
      return `${mm} ${hh} * * *`
    case "weekly":
      return `${mm} ${hh} * * ${dow}`
    case "monthly":
      return `${mm} ${hh} ${dom} * *`
    case "weekday":
      return `${mm} ${hh} * * 1-5`
    case "custom":
      return custom.trim()
  }
}

interface CronForm {
  freq: FreqType
  timeStr: string
  dow: number
  dom: number
  minute: number
  custom: string
}

// best-effort: edit mode maps an expression back onto the preset controls;
// anything unrecognised falls through to "custom" with the raw cron.
function parseCron(cron: string): CronForm {
  const base: CronForm = { freq: "custom", timeStr: "09:00", dow: 1, dom: 1, minute: 0, custom: cron }
  const f = cron.trim().split(/\s+/)
  if (f.length !== 5) return base
  const [min, hour, dom, mon, dow] = f
  const hh = Number(hour)
  const mm = Number(min)
  const timeOK = Number.isInteger(hh) && Number.isInteger(mm)
  const t = fmtTime(hh, mm)
  if (mon === "*" && dom === "*" && dow === "1-5" && timeOK) return { ...base, freq: "weekday", timeStr: t }
  if (mon === "*" && dom === "*" && dow === "*" && hour === "*" && Number.isInteger(mm)) return { ...base, freq: "hourly", minute: mm }
  if (mon === "*" && dom === "*" && dow === "*" && timeOK) return { ...base, freq: "daily", timeStr: t }
  if (mon === "*" && dom === "*" && /^[0-6]$/.test(dow) && timeOK) return { ...base, freq: "weekly", timeStr: t, dow: Number(dow) }
  if (mon === "*" && /^\d{1,2}$/.test(dom) && dow === "*" && timeOK) return { ...base, freq: "monthly", timeStr: t, dom: Number(dom) }
  return base
}

function describeCron(cron: string, t: TFunction<"admin">, weekdays: string[]): string {
  const f = cron.trim().split(/\s+/)
  if (f.length !== 5) return t("scheduledTasks.desc.custom", { cron })
  const [min, hour, dom, mon, dow] = f
  const hh = Number(hour)
  const mm = Number(min)
  const timeOK = Number.isInteger(hh) && Number.isInteger(mm)
  if (mon === "*" && dom === "*" && dow === "1-5" && timeOK) return t("scheduledTasks.desc.weekday", { time: fmtTime(hh, mm) })
  if (mon === "*" && dom === "*" && dow === "*" && hour === "*" && Number.isInteger(mm)) return t("scheduledTasks.desc.hourly", { minute: mm })
  if (mon === "*" && dom === "*" && dow === "*" && timeOK) return t("scheduledTasks.desc.daily", { time: fmtTime(hh, mm) })
  if (mon === "*" && dom === "*" && /^[0-6]$/.test(dow) && timeOK) return t("scheduledTasks.desc.weekly", { day: weekdays[Number(dow)] ?? dow, time: fmtTime(hh, mm) })
  if (mon === "*" && /^\d{1,2}$/.test(dom) && dow === "*" && timeOK) return t("scheduledTasks.desc.monthly", { dom: Number(dom), time: fmtTime(hh, mm) })
  return t("scheduledTasks.desc.custom", { cron })
}

/** The last run's outcome as the ledger's status icon; a task that never ran is "queued". */
function lastStatusIcon(status: string): StatusKind {
  switch (status) {
    case "running":
      return "running"
    case "completed":
      return "completed"
    case "failed":
      return "failed"
    case "cancelled":
    case "skipped_overlap":
      return "cancelled"
    case "interrupted":
    case "auto_disabled":
      return "interrupted"
    default:
      return "queued"
  }
}

function fmtWhen(iso: string | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export function ScheduledTasksPage() {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const workspaceID = useWorkspaceId()
  const [offset, setOffset] = useState(0)
  const tasksQ = useScheduledTasksByWorkspace(workspaceID, { offset, limit: SCHED_PAGE_SIZE })
  const agentsQ = useAgents(workspaceID)
  const createMut = useCreateScheduledTask(workspaceID)
  const updateMut = useUpdateScheduledTask(workspaceID)
  const deleteMut = useDeleteScheduledTask(workspaceID)
  const runNowMut = useRunScheduledTaskNow(workspaceID)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<ScheduledTask | null>(null)
  const [deleting, setDeleting] = useState<ScheduledTask | null>(null)
  const [notice, setNotice] = useState<{ text: string; failed: boolean } | null>(null)

  // Offset is keyed by workspace: switching starts from page one so we
  // never point past the end of the new result set.
  const [offsetKey, setOffsetKey] = useState(workspaceID ?? "")
  if (offsetKey !== (workspaceID ?? "")) {
    setOffsetKey(workspaceID ?? "")
    setOffset(0)
  }

  const weekdays = (t("scheduledTasks.weekdays", { returnObjects: true }) as unknown as string[]) ?? []
  const tasks = tasksQ.data?.scheduled_tasks ?? []
  const total = tasksQ.data?.total ?? 0

  const allAgents = useMemo(() => agentsQ.data?.agents ?? [], [agentsQ.data])
  // active agents are selectable for new tasks; name lookup covers every agent
  // (including disabled) so existing rows still resolve a label.
  const activeAgents = useMemo(() => allAgents.filter((a) => a.status === "active"), [allAgents])
  const agentName = useMemo(() => {
    const m = new Map<string, string>()
    for (const a of allAgents) m.set(a.id, a.name)
    return m
  }, [allAgents])

  function openCreate() {
    setEditing(null)
    setDialogOpen(true)
  }

  function openEdit(task: ScheduledTask) {
    setEditing(task)
    setDialogOpen(true)
  }

  async function toggleEnabled(task: ScheduledTask) {
    await updateMut.mutateAsync({
      taskID: task.id,
      body: {
        name: task.name,
        prompt: task.prompt,
        cron_expr: task.cron_expr,
        timezone: task.timezone,
        enabled: !task.enabled,
      },
    })
  }

  async function runNow(task: ScheduledTask) {
    try {
      await runNowMut.mutateAsync(task.id)
      setNotice({ text: t("scheduledTasks.runNowOk"), failed: false })
    } catch (err) {
      setNotice({ text: err instanceof ApiError ? err.envelope.message : t("scheduledTasks.runNowErr"), failed: true })
    }
  }

  async function confirmDelete() {
    if (!deleting) return
    try {
      await deleteMut.mutateAsync(deleting.id)
      setDeleting(null)
    } catch {
      // The dialog stays open and shows the error.
    }
  }

  const noAgents = !agentsQ.isLoading && activeAgents.length === 0
  const pageTitle = t("scheduledTasks.title")
  const loadError = tasksQ.error
  const unreachable = loadError instanceof ApiError && loadError.envelope.unreachable

  return (
    <AdminLayout activeMenu="scheduled" fullBleed>
      <div className="flex min-w-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={pageTitle}
          subtitleFor="scheduledTasks.title"
          action={
            <Button onClick={openCreate} disabled={!workspaceID || noAgents} data-testid="scheduled-new">
              <Plus strokeWidth={1.5} aria-hidden="true" />
              {t("scheduledTasks.new")}
            </Button>
          }
        />

        {(notice || (noAgents && workspaceID)) && (
          <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-line px-4 text-sm text-fg">
            {notice && !notice.failed ? (
              <Check className="h-3.5 w-3.5 shrink-0 text-status-completed" strokeWidth={1.5} aria-hidden="true" />
            ) : (
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            )}
            <span className="truncate">{notice ? notice.text : t("scheduledTasks.noAgents")}</span>
          </div>
        )}

        {!workspaceID ? (
          <div className="px-6"><ScopeRequiredState scope="workspace" resourceName={pageTitle} /></div>
        ) : tasksQ.isLoading ? (
          <TasksLoadingSkeleton />
        ) : loadError ? (
          <div className="px-6 pt-6">
            <ErrorState
              title={t("scheduledTasks.loadError")}
              description={loadError instanceof Error ? loadError.message : undefined}
              hint={unreachable ? t("runs.loadError.unreachable.hint") : undefined}
              onRetry={() => void tasksQ.refetch()}
            />
          </div>
        ) : tasks.length === 0 ? (
          <EmptyState icon={CalendarClock} title={t("scheduledTasks.empty")} />
        ) : (
          <Ledger columns={LEDGER_COLUMNS} role="list" aria-label={pageTitle}>
            <LedgerHeader>
              <span />
              <span>{t("scheduledTasks.col.name")}</span>
              <span>{t("scheduledTasks.col.frequency")}</span>
              <span>{t("scheduledTasks.dialog.cronLabel")}</span>
              <span>{t("scheduledTasks.dialog.agent")}</span>
              <span className="text-right">{t("scheduledTasks.col.nextRun")}</span>
              <span className="text-right">{t("scheduledTasks.col.lastRun")}</span>
              <span />
            </LedgerHeader>
            <ul className="m-0 list-none p-0">
              {tasks.map((task) => {
                const agent = agentName.get(task.agent_id) ?? task.agent_id
                const statusKey = task.last_status || "none"
                return (
                  <LedgerRow
                    key={task.id}
                    role="listitem"
                    aria-selected={undefined}
                    data-testid="scheduled-row"
                    data-task-name={task.name}
                  >
                    <StatusIcon status={lastStatusIcon(task.last_status)} title={t(`scheduledTasks.status.${statusKey}` as never)} />
                    <span className="flex min-w-0 items-center gap-1.5">
                      <span className="truncate font-medium" title={task.prompt}>{task.name}</span>
                      {!task.enabled && (
                        <Badge variant="neutral" dot className="shrink-0">{t("scheduledTasks.disabled")}</Badge>
                      )}
                    </span>
                    <span className="truncate" title={task.timezone}>
                      {describeCron(task.cron_expr, t, weekdays)}
                      <span className="text-xs text-fg-muted"> · {task.timezone}</span>
                    </span>
                    <LedgerId className="text-fg">{task.cron_expr}</LedgerId>
                    <span className="flex min-w-0 items-center gap-1.5" title={agent}>
                      <InitialTile name={agent} />
                      <span className="truncate">{agent}</span>
                    </span>
                    <LedgerNum muted={!task.next_run_at}>{fmtWhen(task.next_run_at)}</LedgerNum>
                    <LedgerNum muted={!task.last_run_at}>{fmtWhen(task.last_run_at)}</LedgerNum>
                    <RowActions>
                      <ActionIconButton
                        icon={Power}
                        label={task.enabled ? tc("actions.disable") : tc("actions.enable")}
                        busy={updateMut.isPending && updateMut.variables?.taskID === task.id}
                        disabled={updateMut.isPending}
                        onClick={() => void toggleEnabled(task)}
                      />
                      <ActionIconButton
                        icon={Play}
                        label={t("scheduledTasks.action.runNow")}
                        busy={runNowMut.isPending && runNowMut.variables === task.id}
                        disabled={runNowMut.isPending}
                        data-testid="scheduled-run-now"
                        onClick={() => void runNow(task)}
                      />
                      <ActionIconButton icon={Pencil} label={t("scheduledTasks.action.edit")} onClick={() => openEdit(task)} />
                      <ActionIconButton
                        icon={Trash2}
                        label={t("scheduledTasks.action.delete")}
                        tone="danger"
                        onClick={() => {
                          deleteMut.reset()
                          setDeleting(task)
                        }}
                      />
                    </RowActions>
                  </LedgerRow>
                )
              })}
            </ul>
          </Ledger>
        )}

        {workspaceID && !tasksQ.isLoading && !loadError && (
          <OffsetPagination
            offset={offset}
            limit={SCHED_PAGE_SIZE}
            total={total}
            onPrevious={() => setOffset((cur) => Math.max(0, cur - SCHED_PAGE_SIZE))}
            onNext={() => setOffset((cur) => cur + SCHED_PAGE_SIZE)}
          />
        )}
      </div>

      {dialogOpen && (
        <ScheduledTaskDialog
          open={dialogOpen}
          task={editing}
          agents={activeAgents}
          agentName={agentName}
          weekdays={weekdays}
          pending={createMut.isPending || updateMut.isPending}
          error={createMut.error ?? updateMut.error}
          onOpenChange={setDialogOpen}
          onSubmit={async (body, agentID) => {
            if (editing) {
              await updateMut.mutateAsync({
                taskID: editing.id,
                body: {
                  name: body.name,
                  prompt: body.prompt,
                  cron_expr: body.cron_expr,
                  timezone: body.timezone,
                  enabled: editing.enabled,
                },
              })
            } else {
              await createMut.mutateAsync({ agentID, body })
            }
            setDialogOpen(false)
          }}
        />
      )}

      <AlertDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open && !deleteMut.isPending) setDeleting(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{deleting?.name}</AlertDialogTitle>
            <AlertDialogDescription>{t("scheduledTasks.deleteConfirm")}</AlertDialogDescription>
            {deleteMut.error && (
              <p className="flex items-start gap-1.5 break-words text-sm text-fg">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
                <span>{deleteMut.error instanceof ApiError ? deleteMut.error.envelope.message : deleteMut.error.message}</span>
              </p>
            )}
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button variant="outline" disabled={deleteMut.isPending}>{tc("actions.cancel")}</Button>
            </AlertDialogCancel>
            <AlertDialogAction asChild>
              <Button
                variant="destructive"
                disabled={deleteMut.isPending}
                onClick={(event) => {
                  event.preventDefault()
                  void confirmDelete()
                }}
              >
                {deleteMut.isPending && <Loader2 className="animate-spin" />}
                {t("scheduledTasks.action.delete")}
              </Button>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </AdminLayout>
  )
}

function TasksLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3.5 w-3.5 rounded-full" />
          <Skeleton className="h-3 w-40" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-3 w-24" />
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Create / edit dialog                                               */
/* ------------------------------------------------------------------ */

interface DialogProps {
  open: boolean
  task: ScheduledTask | null
  agents: Agent[]
  agentName: Map<string, string>
  weekdays: string[]
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onSubmit: (
    body: { name: string; prompt: string; cron_expr: string; timezone: string },
    agentID: string,
  ) => Promise<void>
}

function ScheduledTaskDialog({ open, task, agents, agentName, weekdays, pending, error, onOpenChange, onSubmit }: DialogProps) {
  const { t } = useTranslation("admin")
  const initial = useMemo<CronForm>(() => (task ? parseCron(task.cron_expr) : { freq: "daily", timeStr: "09:00", dow: 1, dom: 1, minute: 0, custom: "0 9 * * *" }), [task])
  const browserTz = Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai"
  const tzOptions = useMemo(() => timezoneOptions(task?.timezone ?? browserTz), [task, browserTz])

  const [name, setName] = useState(task?.name ?? "")
  const [prompt, setPrompt] = useState(task?.prompt ?? "")
  const [agentID, setAgentID] = useState(task?.agent_id ?? agents[0]?.id ?? "")
  const [freq, setFreq] = useState<FreqType>(initial.freq)
  const [timeStr, setTimeStr] = useState(initial.timeStr)
  const [dow, setDow] = useState(initial.dow)
  const [dom, setDom] = useState(initial.dom)
  const [minute, setMinute] = useState(initial.minute)
  const [custom, setCustom] = useState(initial.custom)
  const [tz, setTz] = useState(task?.timezone ?? browserTz)
  const [localErr, setLocalErr] = useState<string | null>(null)

  const cronExpr = buildCron(freq, timeStr, dow, dom, minute, custom)
  const preview = t("scheduledTasks.desc.withTz", { desc: describeCron(cronExpr, t, weekdays), tz })
  const errMsg = localErr ?? (error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null)

  async function handleSave() {
    setLocalErr(null)
    if (!name.trim()) {
      setLocalErr(t("scheduledTasks.dialog.nameRequired"))
      return
    }
    if (!task && !agentID) {
      setLocalErr(t("scheduledTasks.dialog.agentRequired"))
      return
    }
    if (!prompt.trim()) {
      setLocalErr(t("scheduledTasks.dialog.promptRequired"))
      return
    }
    if (cronExpr.trim().split(/\s+/).length !== 5) {
      setLocalErr(t("scheduledTasks.dialog.cronInvalid"))
      return
    }
    await onSubmit({ name: name.trim(), prompt: prompt.trim(), cron_expr: cronExpr.trim(), timezone: tz.trim() }, agentID)
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!pending) onOpenChange(next) }}>
      <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto overflow-x-hidden">
        <DialogHeader>
          <DialogTitle>{task ? t("scheduledTasks.dialog.editTitle") : t("scheduledTasks.dialog.createTitle")}</DialogTitle>
        </DialogHeader>

        <form
          className="flex flex-col gap-3"
          onSubmit={(event) => {
            event.preventDefault()
            void handleSave()
          }}
        >
          <Field label={t("scheduledTasks.dialog.name")} htmlFor="sched-name">
            <Input id="sched-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={t("scheduledTasks.dialog.namePlaceholder")} disabled={pending} data-testid="scheduled-name" />
          </Field>

          <Field label={t("scheduledTasks.dialog.agent")} htmlFor="sched-agent">
            {task ? (
              <Input id="sched-agent" value={agentName.get(task.agent_id) ?? task.agent_id} disabled readOnly />
            ) : (
              <Select id="sched-agent" value={agentID} onChange={(e) => setAgentID(e.target.value)} disabled={pending} data-testid="scheduled-agent">
                {agents.length === 0 && <option value="">—</option>}
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>{a.name}</option>
                ))}
              </Select>
            )}
          </Field>

          <Field label={t("scheduledTasks.dialog.prompt")} htmlFor="sched-prompt">
            <Textarea
              id="sched-prompt"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder={t("scheduledTasks.dialog.promptPlaceholder")}
              rows={4}
              disabled={pending}
              data-testid="scheduled-prompt"
            />
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label={t("scheduledTasks.dialog.frequency")} htmlFor="sched-freq">
              <Select id="sched-freq" value={freq} onChange={(e) => setFreq(e.target.value as FreqType)} disabled={pending} data-testid="scheduled-freq">
                <option value="daily">{t("scheduledTasks.freq.daily")}</option>
                <option value="weekday">{t("scheduledTasks.freq.weekday")}</option>
                <option value="weekly">{t("scheduledTasks.freq.weekly")}</option>
                <option value="monthly">{t("scheduledTasks.freq.monthly")}</option>
                <option value="hourly">{t("scheduledTasks.freq.hourly")}</option>
                <option value="custom">{t("scheduledTasks.freq.custom")}</option>
              </Select>
            </Field>

            {freq !== "custom" && freq !== "hourly" && (
              <Field label={t("scheduledTasks.dialog.time")} htmlFor="sched-time">
                <Input id="sched-time" type="time" value={timeStr} onChange={(e) => setTimeStr(e.target.value)} disabled={pending} />
              </Field>
            )}

            {freq === "hourly" && (
              <Field label={t("scheduledTasks.dialog.minute")} htmlFor="sched-minute">
                <Input
                  id="sched-minute"
                  type="number"
                  min={0}
                  max={59}
                  value={minute}
                  onChange={(e) => setMinute(Math.max(0, Math.min(59, Number(e.target.value))))}
                  disabled={pending}
                />
              </Field>
            )}

            {freq === "custom" && (
              <Field label={t("scheduledTasks.dialog.cronLabel")} htmlFor="sched-cron">
                <Input
                  id="sched-cron"
                  value={custom}
                  onChange={(e) => setCustom(e.target.value)}
                  placeholder={t("scheduledTasks.dialog.cronPlaceholder")}
                  spellCheck={false}
                  autoCapitalize="off"
                  autoCorrect="off"
                  className="font-mono text-xs"
                  disabled={pending}
                  data-testid="scheduled-cron"
                />
              </Field>
            )}

            {freq === "weekly" && (
              <Field label={t("scheduledTasks.dialog.dayOfWeek")} htmlFor="sched-dow">
                <Select id="sched-dow" value={dow} onChange={(e) => setDow(Number(e.target.value))} disabled={pending}>
                  {weekdays.map((d, idx) => (
                    <option key={idx} value={idx}>{d}</option>
                  ))}
                </Select>
              </Field>
            )}

            {freq === "monthly" && (
              <Field label={t("scheduledTasks.dialog.dayOfMonth")} htmlFor="sched-dom">
                <Select id="sched-dom" value={dom} onChange={(e) => setDom(Number(e.target.value))} disabled={pending}>
                  {Array.from({ length: 28 }, (_, i) => i + 1).map((d) => (
                    <option key={d} value={d}>{d}</option>
                  ))}
                </Select>
              </Field>
            )}

            <Field label={t("scheduledTasks.dialog.timezone")} htmlFor="sched-tz" className={freq === "weekly" || freq === "monthly" ? undefined : "col-span-2"}>
              <Select id="sched-tz" value={tz} onChange={(e) => setTz(e.target.value)} disabled={pending} data-testid="scheduled-tz">
                {tzOptions.map((z) => (
                  <option key={z} value={z}>{z}</option>
                ))}
              </Select>
            </Field>
          </div>

          <PropertyList>
            <Property label={t("scheduledTasks.dialog.preview")} className="h-auto min-h-7 whitespace-normal py-1 [overflow-wrap:anywhere]">
              {preview}
            </Property>
          </PropertyList>

          {errMsg && (
            <p className="flex items-start gap-1.5 break-words text-sm text-fg">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
              <span>{errMsg}</span>
            </p>
          )}

          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
              {t("scheduledTasks.dialog.cancel")}
            </Button>
            <Button type="submit" disabled={pending} data-testid="scheduled-save">
              {pending && <Loader2 className="animate-spin" />}
              {pending ? t("scheduledTasks.dialog.saving") : t("scheduledTasks.dialog.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
