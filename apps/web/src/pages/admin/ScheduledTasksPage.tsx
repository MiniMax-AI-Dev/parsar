import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { Bot, Pencil, Play, Plus, Trash2 } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { Input } from "../../components/ui/input"
import { OffsetPagination } from "../../components/ui/offset-pagination"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
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

function statusVariant(status: string): "success" | "warning" | "destructive" | "neutral" | "primary" {
  switch (status) {
    case "completed":
      return "success"
    case "failed":
      return "destructive"
    case "cancelled":
    case "interrupted":
    case "auto_disabled":
      return "warning"
    case "running":
    case "queued":
      return "primary"
    default:
      return "neutral"
  }
}

function fmtWhen(iso: string | null): string {
  if (!iso) return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function ScheduledTasksPage() {
  const { t } = useTranslation("admin")
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
  const [notice, setNotice] = useState<string | null>(null)

  useEffect(() => {
    setOffset(0)
  }, [workspaceID])

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
      setNotice(t("scheduledTasks.runNowOk"))
    } catch (err) {
      setNotice(err instanceof ApiError ? err.envelope.message : t("scheduledTasks.runNowErr"))
    }
  }

  async function remove(task: ScheduledTask) {
    if (!window.confirm(t("scheduledTasks.deleteConfirm"))) return
    await deleteMut.mutateAsync(task.id)
  }

  const noAgents = !agentsQ.isLoading && activeAgents.length === 0

  return (
    <AdminLayout activeMenu="scheduled">
      <div className="space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-fg">{t("scheduledTasks.title")}</h2>
            <p className="mt-0.5 text-xs text-fg-faint">{t("scheduledTasks.subtitle")}</p>
          </div>
          <Button size="sm" onClick={openCreate} disabled={noAgents} data-testid="scheduled-new">
            <Plus className="mr-1 h-4 w-4" />
            {t("scheduledTasks.new")}
          </Button>
        </div>

        {noAgents && (
          <div className="rounded-md border border-warning-border bg-warning-subtle px-3 py-2 text-xs text-warning break-all">
            {t("scheduledTasks.noAgents")}
          </div>
        )}

        {notice && (
          <div className="rounded-md border border-success-border bg-success-subtle px-3 py-2 text-xs text-success break-all">
            {notice}
          </div>
        )}

        {tasksQ.isLoading ? (
          <p className="text-sm text-fg-faint">…</p>
        ) : tasksQ.error ? (
          <p className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-xs text-danger break-all">
            {t("scheduledTasks.loadError")}
          </p>
        ) : tasks.length === 0 ? (
          <p className="rounded-md bg-surface-muted px-3 py-6 text-center text-sm text-fg-faint">
            {t("scheduledTasks.empty")}
          </p>
        ) : (
          <div className="overflow-hidden rounded-md border border-line">
            {tasks.map((task, i) => (
              <div
                key={task.id}
                data-testid="scheduled-row"
                data-task-name={task.name}
                className={
                  "flex flex-wrap items-center gap-3 px-3 py-2.5 " +
                  (i > 0 ? "border-t border-line-muted" : "")
                }
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate text-xs font-medium text-fg">{task.name}</div>
                  <div className="mt-0.5 text-xs text-fg-faint break-all">
                    {t("scheduledTasks.desc.withTz", {
                      desc: describeCron(task.cron_expr, t, weekdays),
                      tz: task.timezone,
                    })}
                  </div>
                </div>
                <div className="flex w-32 shrink-0 items-center gap-1.5 text-xs text-fg-subtle" title={agentName.get(task.agent_id) ?? task.agent_id}>
                  <Bot className="h-3.5 w-3.5 shrink-0 text-fg-faint" strokeWidth={1.75} />
                  <span className="truncate">{agentName.get(task.agent_id) ?? task.agent_id}</span>
                </div>
                <div className="shrink-0">
                  <Badge variant={statusVariant(task.last_status)}>
                    {t(`scheduledTasks.status.${task.last_status || "none"}` as never)}
                  </Badge>
                </div>
                <div className="w-32 shrink-0 text-xs text-fg-subtle">
                  {task.next_run_at ? fmtWhen(task.next_run_at) : t("scheduledTasks.never")}
                </div>
                <label className="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs text-fg-subtle">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5"
                    checked={task.enabled}
                    onChange={() => void toggleEnabled(task)}
                    disabled={updateMut.isPending}
                  />
                  {task.enabled ? t("scheduledTasks.enabled") : t("scheduledTasks.disabled")}
                </label>
                <div className="flex shrink-0 items-center gap-1">
                  <Button variant="ghost" size="sm" onClick={() => openEdit(task)} title={t("scheduledTasks.action.edit")}>
                    <Pencil className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => void runNow(task)}
                    disabled={runNowMut.isPending}
                    data-testid="scheduled-run-now"
                    title={t("scheduledTasks.action.runNow")}
                  >
                    <Play className="h-3.5 w-3.5" />
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => void remove(task)} title={t("scheduledTasks.action.delete")}>
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}

        <OffsetPagination
          offset={offset}
          limit={SCHED_PAGE_SIZE}
          total={total}
          rangeLabel={({ from, to, total: rangeTotal }) =>
            t("scheduledTasks.pagination.range", { from, to, total: rangeTotal })
          }
          previousLabel={t("scheduledTasks.pagination.prev")}
          nextLabel={t("scheduledTasks.pagination.next")}
          onPrevious={() => setOffset((cur) => Math.max(0, cur - SCHED_PAGE_SIZE))}
          onNext={() => setOffset((cur) => cur + SCHED_PAGE_SIZE)}
          className="text-xs text-fg-subtle"
        />

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
      </div>
    </AdminLayout>
  )
}

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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto overflow-x-hidden sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{task ? t("scheduledTasks.dialog.editTitle") : t("scheduledTasks.dialog.createTitle")}</DialogTitle>
        </DialogHeader>

        <div className="grid gap-3">
          <div className="grid min-w-0 gap-1.5">
            <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.name")}</label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("scheduledTasks.dialog.namePlaceholder")} data-testid="scheduled-name" />
          </div>

          <div className="grid min-w-0 gap-1.5">
            <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.agent")}</label>
            {task ? (
              <Input value={agentName.get(task.agent_id) ?? task.agent_id} disabled readOnly />
            ) : (
              <select
                value={agentID}
                onChange={(e) => setAgentID(e.target.value)}
                data-testid="scheduled-agent"
                className="h-9 rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong"
              >
                {agents.length === 0 && <option value="">—</option>}
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>{a.name}</option>
                ))}
              </select>
            )}
          </div>

          <div className="grid min-w-0 gap-1.5">
            <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.prompt")}</label>
            <textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder={t("scheduledTasks.dialog.promptPlaceholder")}
              rows={4}
              data-testid="scheduled-prompt"
              className="w-full resize-y rounded-md border border-line bg-surface px-3 py-2 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong whitespace-pre-wrap break-all"
            />
          </div>

          <div className="grid min-w-0 gap-1.5">
            <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.frequency")}</label>
            <select
              value={freq}
              onChange={(e) => setFreq(e.target.value as FreqType)}
              data-testid="scheduled-freq"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong"
            >
              <option value="daily">{t("scheduledTasks.freq.daily")}</option>
              <option value="weekday">{t("scheduledTasks.freq.weekday")}</option>
              <option value="weekly">{t("scheduledTasks.freq.weekly")}</option>
              <option value="monthly">{t("scheduledTasks.freq.monthly")}</option>
              <option value="hourly">{t("scheduledTasks.freq.hourly")}</option>
              <option value="custom">{t("scheduledTasks.freq.custom")}</option>
            </select>
          </div>

          {freq !== "custom" && freq !== "hourly" && (
            <div className="grid min-w-0 gap-1.5">
              <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.time")}</label>
              <Input type="time" value={timeStr} onChange={(e) => setTimeStr(e.target.value)} />
            </div>
          )}

          {freq === "weekly" && (
            <div className="grid min-w-0 gap-1.5">
              <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.dayOfWeek")}</label>
              <select
                value={dow}
                onChange={(e) => setDow(Number(e.target.value))}
                className="h-9 rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong"
              >
                {weekdays.map((d, idx) => (
                  <option key={idx} value={idx}>{d}</option>
                ))}
              </select>
            </div>
          )}

          {freq === "monthly" && (
            <div className="grid min-w-0 gap-1.5">
              <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.dayOfMonth")}</label>
              <select
                value={dom}
                onChange={(e) => setDom(Number(e.target.value))}
                className="h-9 rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong"
              >
                {Array.from({ length: 28 }, (_, i) => i + 1).map((d) => (
                  <option key={d} value={d}>{d}</option>
                ))}
              </select>
            </div>
          )}

          {freq === "hourly" && (
            <div className="grid min-w-0 gap-1.5">
              <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.minute")}</label>
              <Input
                type="number"
                min={0}
                max={59}
                value={minute}
                onChange={(e) => setMinute(Math.max(0, Math.min(59, Number(e.target.value))))}
              />
            </div>
          )}

          {freq === "custom" && (
            <div className="grid min-w-0 gap-1.5">
              <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.cronLabel")}</label>
              <Input
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
                placeholder={t("scheduledTasks.dialog.cronPlaceholder")}
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                data-testid="scheduled-cron"
              />
            </div>
          )}

          <div className="grid min-w-0 gap-1.5">
            <label className="text-xs font-medium text-fg-muted">{t("scheduledTasks.dialog.timezone")}</label>
            <select
              value={tz}
              onChange={(e) => setTz(e.target.value)}
              data-testid="scheduled-tz"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong"
            >
              {tzOptions.map((z) => (
                <option key={z} value={z}>{z}</option>
              ))}
            </select>
          </div>

          <p className="rounded-md bg-surface-muted px-3 py-2 text-xs text-fg-subtle whitespace-pre-wrap break-all">
            {t("scheduledTasks.dialog.preview")}: {preview}
          </p>

          <div className="grid min-w-0 gap-0.5 opacity-60">
            <label className="flex items-center gap-2 text-xs text-fg-subtle">
              <input type="checkbox" disabled className="h-3.5 w-3.5" />
              {t("scheduledTasks.dialog.feishu")}
            </label>
            <span className="pl-5 text-xs text-fg-faint">{t("scheduledTasks.dialog.feishuDisabledHint")}</span>
          </div>

          {errMsg && (
            <p className="rounded-md bg-danger-subtle px-3 py-2 text-xs text-danger break-all">{errMsg}</p>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={pending}>
            {t("scheduledTasks.dialog.cancel")}
          </Button>
          <Button size="sm" onClick={() => void handleSave()} disabled={pending} data-testid="scheduled-save">
            {pending ? t("scheduledTasks.dialog.saving") : t("scheduledTasks.dialog.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
