import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  FileText,
  Loader2,
  Search,
  TerminalSquare,
  Wrench,
  X,
  XCircle,
} from "lucide-react"

import { cn } from "../../lib/utils"
import type { ToolStep } from "../../lib/api-types"
import type { StreamingStep } from "../../lib/api-conversations"

const TOOL_ICONS: Record<string, typeof TerminalSquare> = {
  bash: TerminalSquare,
  read: FileText,
  write: FileText,
  edit: FileText,
  grep: Search,
  glob: Search,
}

function toolIcon(name: string) {
  const key = name.toLowerCase()
  return TOOL_ICONS[key] ?? Wrench
}

function displayToolName(name: string): string {
  const match = /^mcp__([^_]+)__(.+)$/i.exec(name.trim())
  if (!match) return (name || "tool").toUpperCase()
  const server = match[1].replace(/[-_]+/g, " ").toUpperCase()
  const tool = match[2].replace(/[-_]+/g, " ").toUpperCase()
  return `${server} · ${tool}`
}

const SUMMARY_MAX = 80

/** Picks the most informative single field from a tool's args payload.
 *  Returns "" when nothing usable; callers hide the detail line then. */
function summarizeArgs(name: string, args?: Record<string, unknown>): string {
  if (!args) return ""
  const key = name.toLowerCase()
  const FIELDS: Record<string, string[]> = {
    bash: ["command"],
    read: ["file_path", "path"],
    write: ["file_path", "path"],
    edit: ["file_path", "path"],
    grep: ["pattern", "query"],
    glob: ["pattern"],
  }
  const candidates = FIELDS[key] ?? []
  for (const field of candidates) {
    const v = args[field]
    if (typeof v === "string" && v.trim() !== "") return v.trim()
  }
  for (const v of Object.values(args)) {
    if (typeof v === "string" && v.trim() !== "") return v.trim()
  }
  return ""
}

// Middle-ellipsis so head + tail both survive (e.g. `find / … -name "vela"`).
function ellipsizeMiddle(text: string, max = SUMMARY_MAX): string {
  if (text.length <= max) return text
  const head = Math.ceil((max - 1) / 2)
  const tail = Math.floor((max - 1) / 2)
  return `${text.slice(0, head)}…${text.slice(text.length - tail)}`
}

function formatElapsed(ms: number): string {
  if (ms < 1000) return `${Math.max(0, Math.round(ms))}ms`
  const totalSec = Math.floor(ms / 1000)
  if (totalSec < 60) return `${totalSec}s`
  const min = Math.floor(totalSec / 60)
  const sec = totalSec % 60
  return sec === 0 ? `${min}m` : `${min}m${sec}s`
}

// 1Hz ticker, only while `active`, so the live working card redraws
// elapsed counters; stops cleanly to avoid leaking timers post-run.
function useElapsedTicker(active: boolean): number {
  const [, setTick] = useState(0)
  useEffect(() => {
    if (!active) return
    const id = window.setInterval(() => setTick((n) => n + 1), 1000)
    return () => window.clearInterval(id)
  }, [active])
  return performance.now()
}

export function StepItem({
  name,
  status,
  detail,
  durationMs,
}: {
  name: string
  status: "running" | "completed" | "failed"
  /** One-line summary from summarizeArgs(); empty hides the detail line. */
  detail?: string
  /** Pass for completed steps; live-tick from caller for running ones. */
  durationMs?: number
}) {
  const Icon = toolIcon(name)
  const upper = displayToolName(name)
  const summary = detail ? ellipsizeMiddle(detail) : ""
  return (
    <div className="flex items-center gap-1.5 py-0.5 text-sm">
      {status === "running" ? (
        <Loader2 className="h-3 w-3 shrink-0 animate-spin text-info" strokeWidth={2.5} />
      ) : status === "failed" ? (
        <XCircle className="h-3 w-3 shrink-0 text-danger" strokeWidth={2.5} />
      ) : (
        <CheckCircle2 className="h-3 w-3 shrink-0 text-success" strokeWidth={2.5} />
      )}
      <Icon className="h-3 w-3 shrink-0 text-fg-subtle" strokeWidth={2} />
      <span
        className={cn(
          "shrink-0 font-medium",
          status === "running"
            ? "text-fg-muted"
            : status === "failed"
              ? "text-danger-emphasis"
              : "text-fg-subtle",
        )}
        title={name}
      >
        {upper}
      </span>
      {summary && (
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg-subtle" title={detail}>
          {summary}
        </span>
      )}
      {!summary && <span className="min-w-0 flex-1" aria-hidden="true" />}
      {durationMs !== undefined && (
        <span className="shrink-0 tabular-nums text-xs text-fg-faint">
          {formatElapsed(durationMs)}
        </span>
      )}
    </div>
  )
}

export function WorkingSteps({
  steps,
  onCancel,
  cancelling,
}: {
  steps: StreamingStep[]
  /** When set, render an X button next to the spinner. Parent owns the runID. */
  onCancel?: () => void
  cancelling?: boolean
}) {
  const { t } = useTranslation("admin")
  const [expanded, setExpanded] = useState(false)
  const anyRunning = steps.some((s) => s.status === "running")
  const now = useElapsedTicker(anyRunning)

  const runningSteps = steps.filter((s) => s.status === "running")
  const completedSteps = steps.filter((s) => s.status === "completed")
  const completedCount = completedSteps.length
  const runningCount = runningSteps.length
  const total = steps.length
  const current = runningSteps[runningSteps.length - 1]

  // From first step's started_at until now (if running) or last ended_at.
  const firstStart = steps.length > 0 ? steps[0].started_at : null
  const lastEnded = !anyRunning
    ? Math.max(...completedSteps.map((s) => s.ended_at ?? s.started_at), 0)
    : null
  const overallMs = firstStart === null ? 0 : (lastEnded ?? now) - firstStart

  return (
    <div className="flex w-fit min-w-[240px] flex-col gap-1 rounded-md bg-surface px-3 py-2 text-sm shadow-sm ring-1 ring-slate-200/70">
      <div className="flex items-center gap-2">
        <button
          type="button"
          aria-expanded={expanded}
          aria-label={
            expanded ? t("conversations.steps.collapseAria") : t("conversations.steps.expandAria")
          }
          onClick={() => setExpanded((v) => !v)}
          className="flex shrink-0 items-center text-fg-faint transition-colors hover:text-fg-muted"
        >
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5" strokeWidth={2.2} />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" strokeWidth={2.2} />
          )}
        </button>
        <div className="min-w-0 flex-1">
          {total > 0 ? (
            <div className="flex items-center gap-2 text-sm">
              <span className="font-medium text-fg-muted">
                {t("conversations.steps.totalLabel", {
                  count: total,
                  defaultValue: "{{count}} steps",
                })}
              </span>
              {completedCount > 0 && (
                <span className="text-success">
                  {t("conversations.steps.doneInline", {
                    count: completedCount,
                    defaultValue: "{{count}} done",
                  })}
                </span>
              )}
              {runningCount > 0 && (
                <span className="text-info">
                  {t("conversations.steps.runningInline", {
                    count: runningCount,
                    defaultValue: "{{count}} running",
                  })}
                </span>
              )}
            </div>
          ) : (
            <div className="flex items-center gap-1.5 text-fg-subtle">
              <Loader2 className="h-3 w-3 animate-spin text-info" strokeWidth={2.5} />
              <span>{t("conversations.steps.working")}</span>
            </div>
          )}
        </div>
        {overallMs > 0 && (
          <span className="shrink-0 tabular-nums text-xs text-fg-faint">
            {formatElapsed(overallMs)}
          </span>
        )}
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={cancelling}
            aria-label={t("conversations.steps.cancelAria", {
              defaultValue: "Cancel current task",
            })}
            title={t("conversations.steps.cancelAria", { defaultValue: "Cancel current task" })}
            className="rounded p-0.5 text-fg-faint transition-colors hover:bg-surface-muted hover:text-danger disabled:opacity-40"
          >
            {cancelling ? (
              <Loader2 className="h-3 w-3 animate-spin" strokeWidth={2.5} />
            ) : (
              <X className="h-3 w-3" strokeWidth={2.5} />
            )}
          </button>
        )}
      </div>

      {/* Collapsed view shows just the current step; expanded view lists all. */}
      {!expanded && current && (
        <StepItem
          name={current.name}
          status="running"
          detail={summarizeArgs(current.name, current.args)}
          durationMs={Math.max(0, now - current.started_at)}
        />
      )}

      {expanded && total > 0 && (
        <div className="mt-0.5 space-y-0 border-l border-line pl-2">
          {steps.map((s) => {
            const isRunning = s.status === "running"
            const baseMs = isRunning
              ? now - s.started_at
              : (s.ended_at ?? s.started_at) - s.started_at
            return (
              <StepItem
                key={s.tool_call_id}
                name={s.name}
                status={s.status}
                detail={summarizeArgs(s.name, s.args)}
                durationMs={Math.max(0, baseMs)}
              />
            )
          })}
        </div>
      )}
    </div>
  )
}

export function StepTrace({ steps }: { steps: ToolStep[] }) {
  const { t } = useTranslation("admin")
  const [expanded, setExpanded] = useState(false)

  if (steps.length === 0) return null

  return (
    <div className="mt-2">
      <button
        type="button"
        aria-expanded={expanded}
        aria-label={
          expanded ? t("conversations.steps.collapseAria") : t("conversations.steps.expandAria")
        }
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-1 rounded-md px-1.5 py-1 text-xs font-medium text-fg-subtle transition-colors hover:bg-surface-muted hover:text-fg-muted"
      >
        {expanded ? (
          <ChevronDown className="h-3 w-3" strokeWidth={2.2} />
        ) : (
          <ChevronRight className="h-3 w-3" strokeWidth={2.2} />
        )}
        {t("conversations.steps.traceLabel", { count: steps.length })}
      </button>
      {expanded && (
        <div className="mt-1 space-y-0 border-l border-line pl-3">
          {steps.map((step, i) => (
            <StepItem
              key={step.tool_call_id || i}
              name={step.name}
              status={step.status}
              detail={summarizeArgs(step.name, step.args)}
            />
          ))}
        </div>
      )}
    </div>
  )
}
