import { useEffect, useRef, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { Check, Loader2, Square, Terminal, Wrench, X } from "lucide-react"

import { useResolveAgentInteraction } from "../../lib/api-interactions"
import type { AgentInteraction } from "../../lib/api-types"
import { cn } from "../../lib/utils"
import { Button } from "../ui/button"
import { InlineError } from "../ui/error-state"

/** `m:ss` from the interaction deadline; never below 0:00. */
function formatCountdown(remainingMs: number): string {
  const total = Math.max(0, Math.ceil(remainingMs / 1000))
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  return `${minutes}:${String(seconds).padStart(2, "0")}`
}

function useCountdown(deadlineISO: string | undefined): number {
  const deadline = deadlineISO ? Date.parse(deadlineISO) : NaN
  const [remaining, setRemaining] = useState(() => (isNaN(deadline) ? 0 : deadline - Date.now()))
  useEffect(() => {
    if (isNaN(deadline)) return
    const tick = () => setRemaining(deadline - Date.now())
    tick()
    const id = window.setInterval(tick, 1000)
    return () => window.clearInterval(id)
  }, [deadline])
  return isNaN(deadline) ? NaN : remaining
}

function str(value: unknown): string {
  return typeof value === "string" ? value : ""
}

/** Tool name and the command / resource line, read from the request. */
function describe(interaction: AgentInteraction, unknownTool: string) {
  const request = interaction.request
  const payload = (request.payload ?? {}) as Record<string, unknown>
  const tool = str(payload.tool) || str(request.action) || str(request.tool) || unknownTool
  const summary = str(request.resource) || str(payload.command) || str(request.detail)
  const detail = str(request.detail)
  return { tool, summary, detail }
}

type Decision = "allow" | "deny"

/**
 * The approval that replaces the composer while an agent waits on a
 * permission: tool name and a countdown on the first line, the one sentence
 * that names the request, the command in mono, then 拒绝 · 允许一次.
 * Enter allows once and Escape denies while focus is inside; focus lands
 * here on mount. Oldest request first; the count shows when several wait.
 */
export function ApprovalBar({
  interactions,
  workspaceID,
  className,
  stop,
}: {
  /** Pending permission interactions of this conversation, oldest first. */
  interactions: AgentInteraction[]
  workspaceID: string
  className?: string
  /**
   * The conversation's single cancel control while the bar owns the
   * composer slot: the same round ink stop button the composer shows.
   */
  stop?: { onStop: () => void; pending?: boolean; label: string }
}) {
  const { t } = useTranslation("admin")
  const resolve = useResolveAgentInteraction(workspaceID)
  const [submitting, setSubmitting] = useState<Decision | null>(null)
  const [error, setError] = useState<{ id: string; message: string } | null>(null)
  const ref = useRef<HTMLElement>(null)

  const current = interactions[0]
  const currentID = current?.id ?? ""
  const remaining = useCountdown(current?.expires_at)

  useEffect(() => {
    if (!currentID) return
    ref.current?.focus({ preventScroll: true })
  }, [currentID])

  if (!current) return null

  const { tool, summary, detail } = describe(current, t("conversations.permission.unknownTool"))
  const ToolIcon = tool.toLowerCase() === "bash" ? Terminal : Wrench
  const agent = current.agent_name || t("approvals.detail.agent")
  const errorText = error?.id === current.id ? error.message : ""
  const busy = submitting !== null

  const decide = async (decision: Decision) => {
    if (busy) return
    setSubmitting(decision)
    setError(null)
    try {
      await resolve.mutateAsync({ id: current.id, body: { approved: decision === "allow" } })
    } catch (err) {
      setError({ id: current.id, message: err instanceof Error ? err.message : String(err) })
    } finally {
      setSubmitting(null)
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (
      event.defaultPrevented ||
      event.nativeEvent.isComposing ||
      event.repeat ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      busy
    ) {
      return
    }
    if (event.key === "Escape") {
      event.preventDefault()
      event.stopPropagation()
      void decide("deny")
      return
    }
    const target = event.target
    if (event.key !== "Enter" || (target instanceof Element && target.closest("button,a,input,textarea,select"))) {
      return
    }
    event.preventDefault()
    event.stopPropagation()
    void decide("allow")
  }

  return (
    <section
      ref={ref}
      tabIndex={-1}
      aria-label={t("conversations.approval.title")}
      onKeyDown={onKeyDown}
      className={cn("flex flex-col outline-none", className)}
      data-testid="interaction-card"
      data-interaction-kind={current.kind}
      data-request-id={current.request_id}
    >
      <div className="flex h-7 items-center gap-2 text-xs text-fg-muted">
        <ToolIcon className="h-3.5 w-3.5 shrink-0" strokeWidth={1.5} aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate">{tool}</span>
        {!isNaN(remaining) && (
          <span role="timer" className="shrink-0 font-mono tabular-nums">
            {interactions.length > 1 ? `1 / ${interactions.length} · ` : ""}
            {formatCountdown(remaining)}
          </span>
        )}
      </div>

      <p className="m-0 mt-1 text-sm text-fg">
        {t("conversations.approval.body", { agent, tool })}
      </p>
      {summary && (
        <p
          className="m-0 mt-1 truncate font-mono text-xs text-fg-muted"
          title={detail && detail !== summary ? `${summary}\n${detail}` : summary}
        >
          {summary}
        </p>
      )}

      <div className="mt-3 flex items-center justify-end gap-2">
        {errorText && <InlineError className="mr-auto min-w-0 flex-1">{errorText}</InlineError>}
        <Button
          variant="outline"
          disabled={busy}
          aria-busy={submitting === "deny"}
          onClick={() => void decide("deny")}
        >
          {submitting === "deny" ? (
            <Loader2 className="animate-spin" aria-hidden="true" />
          ) : (
            <X strokeWidth={1.5} aria-hidden="true" />
          )}
          {t("approvals.actions.deny")}
        </Button>
        <Button disabled={busy} aria-busy={submitting === "allow"} onClick={() => void decide("allow")}>
          {submitting === "allow" ? (
            <Loader2 className="animate-spin" aria-hidden="true" />
          ) : (
            <Check strokeWidth={1.5} aria-hidden="true" />
          )}
          {t("approvals.actions.allowOnce")}
        </Button>
        {stop && (
          <button
            type="button"
            onClick={stop.onStop}
            disabled={stop.pending}
            aria-label={stop.label}
            title={stop.label}
            className="ml-1 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis active:scale-[0.97] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-50"
          >
            {stop.pending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
            ) : (
              <Square className="h-3 w-3 fill-current" strokeWidth={1.5} aria-hidden="true" />
            )}
          </button>
        )}
      </div>
    </section>
  )
}
