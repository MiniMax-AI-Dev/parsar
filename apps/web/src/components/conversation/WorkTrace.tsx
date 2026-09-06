/* eslint-disable react-refresh/only-export-components */
import { useEffect, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { ChevronDown, ChevronRight, FileText, Search, TerminalSquare, Wrench, type LucideIcon } from "lucide-react"

import { StatusIcon, type StatusKind } from "../ui/status-icon"
import { cn } from "../../lib/utils"

/**
 * One tool call or step of a run, normalised from the timeline's `ToolStep`
 * and the SSE `StreamingStep` so the trace reads the same live and after.
 * Timestamps are epoch milliseconds; a missing `endedAt` on a completed step
 * means the source never recorded one and the row shows no duration.
 */
export interface TraceStep {
  id: string
  name: string
  status: "running" | "completed" | "failed"
  args?: Record<string, unknown>
  result?: unknown
  startedAt?: number
  endedAt?: number
}

const COLLAPSE_MS = 220

/* ------------------------------------------------------------------ */
/*  Small helpers                                                      */
/* ------------------------------------------------------------------ */

type Category = "bash" | "read" | "write" | "edit" | "search" | "tool"

const CATEGORY_ICON: Record<Category, LucideIcon> = {
  bash: TerminalSquare,
  read: FileText,
  write: FileText,
  edit: FileText,
  search: Search,
  tool: Wrench,
}

const TARGET_FIELDS: Record<Category, string[]> = {
  bash: ["command"],
  read: ["file_path", "path"],
  write: ["file_path", "path"],
  edit: ["file_path", "path"],
  search: ["pattern", "query"],
  tool: [],
}

function categoryOf(name: string): Category {
  const key = name.toLowerCase()
  if (key === "bash" || key === "shell" || key === "exec") return "bash"
  if (key === "read" || key === "read_file") return "read"
  if (key === "write" || key === "write_file") return "write"
  if (key === "edit" || key === "edit_file" || key === "multiedit") return "edit"
  if (key === "grep" || key === "glob" || key === "search") return "search"
  return "tool"
}

/** The one argument worth showing on the row: a command, a path, a pattern. */
function targetOf(category: Category, args?: Record<string, unknown>): string {
  if (!args) return ""
  for (const field of TARGET_FIELDS[category]) {
    const value = args[field]
    if (typeof value === "string" && value.trim() !== "") return value.trim()
  }
  for (const value of Object.values(args)) {
    if (typeof value === "string" && value.trim() !== "") return value.trim()
  }
  return ""
}

/** "12s", "1m 03s", "1h 02m". Empty under one second. */
export function formatTraceElapsed(ms: number): string {
  const total = Math.floor(Math.max(0, ms) / 1000)
  if (total < 1) return ""
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  if (hours > 0) return `${hours}h ${String(minutes).padStart(2, "0")}m`
  if (minutes > 0) return `${minutes}m ${String(seconds).padStart(2, "0")}s`
  return `${seconds}s`
}

function stringify(value: unknown): string {
  if (typeof value === "string") return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

/** 1Hz clock while `active`; 0 otherwise so callers can branch on it. */
function useSecondTick(active: boolean): number {
  const [now, setNow] = useState(() => (active ? Date.now() : 0))
  useEffect(() => {
    if (!active) return
    const update = () => setNow(Date.now())
    update()
    const id = window.setInterval(update, 1000)
    return () => window.clearInterval(id)
  }, [active])
  return now
}

/**
 * Disclosure that belongs to the user once it has opened: attention opens
 * it, completion of an answered run closes it once, and neither fights a
 * later manual toggle.
 */
function useAttentionDisclosure(attentionRequired: boolean, defaultOpen: boolean) {
  const [open, setOpen] = useState(() => defaultOpen || attentionRequired)
  const [seenAttention, setSeenAttention] = useState(attentionRequired)
  if (attentionRequired !== seenAttention) {
    setSeenAttention(attentionRequired)
    if (attentionRequired) setOpen(true)
  }
  return [open, setOpen] as const
}

/* ------------------------------------------------------------------ */
/*  LazyCollapse: mounts the body on first open, keeps it through the   */
/*  exit motion, then unmounts so large results never sit in the DOM.   */
/* ------------------------------------------------------------------ */

export function LazyCollapse({
  open,
  className,
  children,
}: {
  open: boolean
  className?: string
  children: () => ReactNode
}) {
  const [mounted, setMounted] = useState(open)
  if (open && !mounted) setMounted(true)
  useEffect(() => {
    if (open || !mounted) return
    const timer = window.setTimeout(() => setMounted(false), COLLAPSE_MS)
    return () => window.clearTimeout(timer)
  }, [open, mounted])

  return (
    <div
      aria-hidden={!open}
      className={cn(
        "grid h-min content-start transition-[grid-template-rows] duration-[220ms] ease-settle",
        open ? "grid-rows-[1fr]" : "pointer-events-none grid-rows-[0fr]",
        className,
      )}
    >
      <div className="min-h-0 overflow-hidden">
        {open || mounted ? (
          <div
            className={cn(
              "origin-top transition-[opacity,transform] duration-200 ease-settle",
              open ? "translate-y-0 opacity-100" : "-translate-y-1 opacity-0",
            )}
          >
            {children()}
          </div>
        ) : null}
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  StepRow: 32px hairline row, verb in ink + target in mono muted,     */
/*  chevron on the right, args / result revealed beneath on demand.     */
/* ------------------------------------------------------------------ */

function StepRow({ step, now }: { step: TraceStep; now: number }) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const category = categoryOf(step.name)
  const Icon = CATEGORY_ICON[category]
  const verb = category === "tool" ? t("conversations.trace.verb.tool") : t(`conversations.trace.verb.${category}`)
  const argTarget = targetOf(category, step.args)
  const target = category === "tool" ? [step.name, argTarget].filter(Boolean).join(" ") : argTarget
  const hasArgs = !!step.args && Object.keys(step.args).length > 0
  const hasResult = step.result !== undefined && step.result !== null && step.status !== "running"
  const expandable = hasArgs || hasResult

  const elapsedMs =
    step.startedAt === undefined
      ? undefined
      : step.status === "running"
        ? now > 0
          ? now - step.startedAt
          : undefined
        : step.endedAt !== undefined
          ? step.endedAt - step.startedAt
          : undefined
  const elapsed = elapsedMs === undefined ? "" : formatTraceElapsed(elapsedMs)

  const row = (
    <>
      <Icon className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
      <span className="shrink-0 text-sm text-fg">{verb}</span>
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg-muted" title={target || undefined}>
        {target}
      </span>
      {step.status !== "completed" && <StatusIcon status={step.status} />}
      {elapsed && <span className="shrink-0 font-mono text-xs tabular-nums text-fg-muted">{elapsed}</span>}
      {expandable && (
        <ChevronRight
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-fg-muted transition-transform duration-150 ease-settle",
            open && "rotate-90",
          )}
          strokeWidth={1.5}
          aria-hidden="true"
        />
      )}
    </>
  )

  return (
    <li className="border-b border-line last:border-b-0" data-trace-step={step.id}>
      {expandable ? (
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
          className="flex h-8 w-full items-center gap-2 text-left hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/40"
        >
          {row}
        </button>
      ) : (
        <div className="flex h-8 w-full items-center gap-2">{row}</div>
      )}
      {expandable && (
        <LazyCollapse open={open}>
          {() => (
            <div className="space-y-2 pb-2 pl-5">
              {hasArgs && (
                <div>
                  <p className="m-0 mb-1 text-xs text-fg-muted">{t("conversations.trace.args")}</p>
                  <pre className="m-0 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
                    {stringify(step.args)}
                  </pre>
                </div>
              )}
              {hasResult && (
                <div>
                  <p className="m-0 mb-1 text-xs text-fg-muted">{t("conversations.trace.result")}</p>
                  <pre className="m-0 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
                    {stringify(step.result)}
                  </pre>
                </div>
              )}
            </div>
          )}
        </LazyCollapse>
      )}
    </li>
  )
}

/* ------------------------------------------------------------------ */
/*  WorkTrace                                                          */
/* ------------------------------------------------------------------ */

const DONE_LABEL = {
  completed: "conversations.trace.completed",
  failed: "conversations.trace.failed",
  cancelled: "conversations.trace.cancelled",
  interrupted: "conversations.trace.interrupted",
} as const satisfies Record<Exclude<StatusKind, "running" | "queued">, string>

/**
 * The block between a user turn and the assistant's answer: one header row
 * ("运行中 · 12s" while running, "已完成 · 1m 03s" after) whose hairline
 * runs to the right edge, and beneath it the run's steps as 32px rows.
 * Open while running or when a step waits on the user; folds once the
 * answer lands; while folded and still running, the current step stays
 * visible as a single tail line.
 */
export function WorkTrace({
  steps,
  status,
  startedAt,
  finishedAt,
  attentionRequired = false,
  collapseAfterAnswer = true,
  className,
}: {
  steps: TraceStep[]
  /** Run status; `running` keeps the clock ticking and the block open. */
  status: StatusKind
  /** Epoch ms; the header clock counts from here. */
  startedAt?: number
  finishedAt?: number
  /** A step waits on the user (a pending approval for this run): stay open. */
  attentionRequired?: boolean
  /** Fold once the run stops. Default on; the user may reopen it. */
  collapseAfterAnswer?: boolean
  className?: string
}) {
  const { t } = useTranslation("admin")
  const running = status === "running" || status === "queued"
  const hasDetails = steps.length > 0
  const [expanded, setExpanded] = useAttentionDisclosure(attentionRequired, running)

  useEffect(() => {
    if (!running && !attentionRequired && collapseAfterAnswer) setExpanded(false)
  }, [running, attentionRequired, collapseAfterAnswer, setExpanded])

  const now = useSecondTick(running)

  // Elapsed: the run's own clock when the timeline has it; otherwise the
  // span of its steps; nothing when neither is known.
  const firstStep = steps.find((s) => s.startedAt !== undefined)?.startedAt
  const lastStep = steps.reduce<number | undefined>((acc, s) => {
    const end = s.endedAt ?? s.startedAt
    return end === undefined ? acc : acc === undefined ? end : Math.max(acc, end)
  }, undefined)
  const start = startedAt ?? firstStep
  const end = running ? (now > 0 ? now : undefined) : (finishedAt ?? lastStep)
  const elapsed = start !== undefined && end !== undefined ? formatTraceElapsed(end - start) : ""

  const word =
    status === "running" || status === "queued"
      ? t("conversations.trace.running")
      : t(DONE_LABEL[status])
  const current = running ? [...steps].reverse().find((s) => s.status === "running") : undefined

  const header = (
    <>
      <StatusIcon status={status} />
      <span className="shrink-0 text-sm text-fg-muted">{word}</span>
      {elapsed && (
        <span className="shrink-0 text-sm text-fg-muted">
          <span aria-hidden="true">· </span>
          <span className="font-mono text-xs tabular-nums">{elapsed}</span>
        </span>
      )}
      {hasDetails && (
        <ChevronDown
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-fg-muted opacity-0 transition-[opacity,transform] duration-150 ease-settle group-hover/trace:opacity-100 group-focus-visible/trace:opacity-100",
            !expanded && "-rotate-90",
          )}
          strokeWidth={1.5}
          aria-hidden="true"
        />
      )}
    </>
  )

  return (
    <section
      className={cn("min-w-0", className)}
      aria-label={t("conversations.trace.label")}
      aria-busy={running && !attentionRequired}
      data-work-trace={status}
    >
      {hasDetails ? (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
          className="group/trace flex h-8 w-full items-center gap-2 rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/40"
        >
          {header}
        </button>
      ) : (
        <div className="flex h-8 w-full items-center gap-2" role="status" aria-live="polite">
          {header}
        </div>
      )}

      {hasDetails && (
        <LazyCollapse open={expanded}>
          {() => (
            <ul className="m-0 list-none border-t border-line p-0">
              {steps.map((step) => (
                <StepRow key={step.id} step={step} now={now} />
              ))}
            </ul>
          )}
        </LazyCollapse>
      )}

      {hasDetails && !expanded && running && current && (
        <ul className="m-0 list-none border-t border-line p-0" data-work-trace-tail="">
          <StepRow key={`tail-${current.id}`} step={current} now={now} />
        </ul>
      )}
    </section>
  )
}
