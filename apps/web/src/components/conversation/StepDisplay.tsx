import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  ChevronDown,
  ChevronRight,
  FileText,
  Loader2,
  Search,
  TerminalSquare,
  Wrench,
  X,
} from "lucide-react"

import { Button } from "../ui/button"
import { StatusIcon } from "../ui/status-icon"
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

function ToolIcon({ name }: { name: string }) {
  const Icon = TOOL_ICONS[name.toLowerCase()] ?? Wrench
  return <Icon className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
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
  const [now, setNow] = useState(0)
  useEffect(() => {
    if (!active) return
    const update = () => setNow(performance.now())
    const startID = window.setTimeout(update, 0)
    const id = window.setInterval(update, 1000)
    return () => {
      window.clearTimeout(startID)
      window.clearInterval(id)
    }
  }, [active])
  return now
}

/**
 * One tool step as a 32px hairline row: status icon, tool icon, the tool
 * name in ink, its one-line argument summary in muted mono, and the
 * duration right-aligned. State lives in the icon only.
 */
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
  const upper = (name || "tool").toUpperCase()
  const summary = detail ? ellipsizeMiddle(detail) : ""
  return (
    <div className="flex h-8 items-center gap-2 border-b border-line text-sm last:border-b-0">
      <StatusIcon status={status} />
      <ToolIcon name={name} />
      <span className="shrink-0 font-medium text-fg">{upper}</span>
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg-muted" title={detail}>
        {summary}
      </span>
      {durationMs !== undefined && (
        <span className="shrink-0 font-mono text-xs tabular-nums text-fg-muted">
          {formatElapsed(durationMs)}
        </span>
      )}
    </div>
  )
}

export function WorkingSteps({
  steps,
  active,
  onCancel,
  cancelling,
}: {
  steps: StreamingStep[]
  active: boolean
  /** When set, render an X button next to the spinner. Parent owns the runID. */
  onCancel?: () => void
  cancelling?: boolean
}) {
  const { t } = useTranslation("admin")
  const [expanded, setExpanded] = useState(false)
  const now = useElapsedTicker(active)

  const runningSteps = steps.filter((s) => s.status === "running")
  const completedSteps = steps.filter((s) => s.status === "completed")
  const completedCount = completedSteps.length
  const runningCount = runningSteps.length
  const total = steps.length
  const current = runningSteps[runningSteps.length - 1]

  // From first step's started_at until now (if running) or last ended_at.
  const firstStart = steps.length > 0 ? steps[0].started_at : null
  const lastEnded = !active
    ? Math.max(...completedSteps.map((s) => s.ended_at ?? s.started_at), 0)
    : null
  const overallMs = firstStart === null ? 0 : (lastEnded ?? now) - firstStart

  const toggleLabel = expanded
    ? t("conversations.steps.collapseAria")
    : t("conversations.steps.expandAria")
  const cancelLabel = t("conversations.steps.cancelAria", { defaultValue: "Cancel current task" })

  return (
    <div className="text-sm">
      <div className="flex h-8 items-center gap-2 border-b border-line">
        <Button
          variant="ghost"
          size="icon"
          className="-ml-1.5 h-6 w-6"
          aria-expanded={expanded}
          aria-label={toggleLabel}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? <ChevronDown strokeWidth={1.5} /> : <ChevronRight strokeWidth={1.5} />}
        </Button>
        {active && <StatusIcon status="running" />}
        <span className="font-medium text-fg">
          {active
            ? t("conversations.steps.working")
            : t("conversations.steps.totalLabel", { count: total, defaultValue: "{{count}} steps" })}
        </span>
        <span className="min-w-0 flex-1 truncate text-xs text-fg-muted">
          {active && completedCount > 0 &&
            t("conversations.steps.completedInline", { count: completedCount, defaultValue: "{{count}} completed" })}
          {active && completedCount > 0 && runningCount > 0 && " · "}
          {active && runningCount > 0 &&
            t("conversations.steps.runningInline", { count: runningCount, defaultValue: "{{count}} running" })}
          {!active && completedCount > 0 &&
            t("conversations.steps.doneInline", { count: completedCount, defaultValue: "{{count}} done" })}
        </span>
        {overallMs > 0 && (
          <span className="shrink-0 font-mono text-xs tabular-nums text-fg-muted">
            {formatElapsed(overallMs)}
          </span>
        )}
        {onCancel && (
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={onCancel}
            disabled={cancelling}
            aria-label={cancelLabel}
            title={cancelLabel}
          >
            {cancelling ? <Loader2 className="animate-spin" strokeWidth={1.5} /> : <X strokeWidth={1.5} />}
          </Button>
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

      {expanded &&
        steps.map((s) => {
          const isRunning = s.status === "running"
          const baseMs = isRunning ? now - s.started_at : (s.ended_at ?? s.started_at) - s.started_at
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
  )
}

export function StepTrace({ steps }: { steps: ToolStep[] }) {
  const { t } = useTranslation("admin")
  const [expanded, setExpanded] = useState(false)

  if (steps.length === 0) return null

  return (
    <div className="mt-2">
      <Button
        variant="ghost"
        size="sm"
        className="-ml-2"
        aria-expanded={expanded}
        onClick={() => setExpanded((v) => !v)}
      >
        {expanded ? <ChevronDown strokeWidth={1.5} /> : <ChevronRight strokeWidth={1.5} />}
        {t("conversations.steps.traceLabel", { count: steps.length })}
      </Button>
      {expanded && (
        <div className="mt-1 border-t border-line">
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
