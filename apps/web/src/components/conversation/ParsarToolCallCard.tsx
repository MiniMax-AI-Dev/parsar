import { useState } from "react"
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
  XCircle,
} from "lucide-react"

import { cn } from "../../lib/utils"
import { useToolCallElapsed } from "@assistant-ui/react"

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
// ParsarToolCallCard — renders inside assistant-ui's message parts
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

  return (
    <div className="my-1 rounded-md border border-line/60 bg-surface shadow-sm">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-sm transition-colors hover:bg-surface-subtle"
        aria-expanded={expanded}
      >
        {expanded ? (
          <ChevronDown className="h-3 w-3 shrink-0 text-fg-faint" strokeWidth={2.2} />
        ) : (
          <ChevronRight className="h-3 w-3 shrink-0 text-fg-faint" strokeWidth={2.2} />
        )}

        {isRunning ? (
          <Loader2 className="h-3 w-3 shrink-0 animate-spin text-info" strokeWidth={2.5} />
        ) : isError ? (
          <XCircle className="h-3 w-3 shrink-0 text-danger" strokeWidth={2.5} />
        ) : (
          <CheckCircle2 className="h-3 w-3 shrink-0 text-success" strokeWidth={2.5} />
        )}

        <IconComponent className="h-3 w-3 shrink-0 text-fg-subtle" strokeWidth={2} />
        <span
          className={cn(
            "shrink-0 font-medium",
            isRunning ? "text-fg-muted" : isError ? "text-danger-emphasis" : "text-fg-subtle",
          )}
        >
          {upper}
        </span>

        {summaryDisplay && (
          <span
            className="min-w-0 flex-1 truncate font-mono text-xs text-fg-subtle"
            title={summary}
          >
            {summaryDisplay}
          </span>
        )}
        {!summaryDisplay && <span className="min-w-0 flex-1" aria-hidden="true" />}

        {elapsed != null && (
          <span className="shrink-0 tabular-nums text-xs text-fg-faint">
            {formatElapsed(elapsed)}
          </span>
        )}
      </button>

      {expanded && (
        <div className="border-t border-line/40 px-3 py-2 text-xs">
          {args && Object.keys(args).length > 0 && (
            <div className="mb-2">
              <p className="mb-1 font-medium text-fg-subtle">
                {t("conversations.toolCall.argsLabel", { defaultValue: "Arguments" })}
              </p>
              <pre className="max-h-32 overflow-auto rounded bg-surface-subtle p-2 text-fg-muted">
                {JSON.stringify(args, null, 2)}
              </pre>
            </div>
          )}
          {result != null && !isRunning && (
            <div>
              <p className="mb-1 font-medium text-fg-subtle">
                {t("conversations.toolCall.resultLabel", { defaultValue: "Result" })}
              </p>
              <pre className="max-h-32 overflow-auto rounded bg-surface-subtle p-2 text-fg-muted">
                {typeof result === "string" ? result : JSON.stringify(result, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
