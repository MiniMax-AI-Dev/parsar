import { useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronDown, ChevronRight, FileText, Search, TerminalSquare, Wrench } from "lucide-react"
import { useToolCallElapsed } from "@assistant-ui/react"

import { StatusIcon } from "../ui/status-icon"

// ---------------------------------------------------------------------------
// Tool icon / summary helpers (mirrored from StepDisplay to keep styling)
// ---------------------------------------------------------------------------

const TOOL_ICONS: Record<string, typeof TerminalSquare> = {
  bash: TerminalSquare,
  read: FileText,
  write: FileText,
  edit: FileText,
  grep: Search,
  glob: Search,
}

const SUMMARY_MAX = 80

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

// ---------------------------------------------------------------------------
// ParsarToolCallCard — a 32px hairline row inside assistant-ui's message
// parts: status icon, tool icon, tool name in ink, argument summary in
// muted mono, elapsed time. Click to reveal arguments and result.
// ---------------------------------------------------------------------------

export function ParsarToolCallCard({
  toolName,
  args,
  result,
  status,
}: {
  toolName: string
  args: Record<string, unknown>
  result?: unknown
  status: { type: string; reason?: string }
}) {
  const { t } = useTranslation("admin")
  const [expanded, setExpanded] = useState(false)
  const elapsed = useToolCallElapsed()

  const upper = (toolName || "tool").toUpperCase()
  const summary = summarizeArgs(toolName, args)
  const summaryDisplay = summary ? ellipsizeMiddle(summary) : ""
  const IconComponent = TOOL_ICONS[toolName.toLowerCase()] ?? Wrench

  const isRunning = status.type === "running"
  const isError = status.type === "incomplete" && status.reason === "error"
  const stepStatus = isRunning ? "running" : isError ? "failed" : "completed"
  const Chevron = expanded ? ChevronDown : ChevronRight

  return (
    <div className="border-b border-line text-sm last:border-b-0">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex h-8 w-full items-center gap-2 text-left hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/40"
        aria-expanded={expanded}
      >
        <Chevron className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <StatusIcon status={stepStatus} />
        <IconComponent className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <span className="shrink-0 font-medium text-fg">{upper}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg-muted" title={summary || undefined}>
          {summaryDisplay}
        </span>
        {elapsed != null && (
          <span className="shrink-0 font-mono text-xs tabular-nums text-fg-muted">{formatElapsed(elapsed)}</span>
        )}
      </button>

      {expanded && (
        <div className="space-y-2 pb-2 pl-5">
          {args && Object.keys(args).length > 0 && (
            <div>
              <p className="m-0 mb-1 text-xs text-fg-muted">
                {t("conversations.toolCall.argsLabel", { defaultValue: "Arguments" })}
              </p>
              <pre className="m-0 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
                {JSON.stringify(args, null, 2)}
              </pre>
            </div>
          )}
          {result != null && !isRunning && (
            <div>
              <p className="m-0 mb-1 text-xs text-fg-muted">
                {t("conversations.toolCall.resultLabel", { defaultValue: "Result" })}
              </p>
              <pre className="m-0 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
                {typeof result === "string" ? result : JSON.stringify(result, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
