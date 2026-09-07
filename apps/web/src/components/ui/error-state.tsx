import * as React from "react"
import { AlertTriangle, Info, RefreshCw } from "lucide-react"
import type { ReactNode } from "react"
import { StatusIcon } from "./status-icon"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { Button } from "./button"

interface ErrorStateProps {
  title?: string
  description?: string
  hint?: string
  onRetry?: () => void
  action?: ReactNode
  className?: string
}

/**
 * Flat error state: a failed-status icon, an ink title, the message and
 * hint in muted, one retry button. No red box; colour lives in the icon.
 */
export function ErrorState({
  title,
  description,
  hint,
  onRetry,
  action,
  className,
}: ErrorStateProps) {
  const { t } = useTranslation("common")
  const resolvedTitle = title ?? t("errors.loadFailed", { defaultValue: "Failed to load" })
  return (
    <div className={cn("flex flex-col items-start gap-3 py-4 text-sm", className)}>
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
        <div className="space-y-1">
          <p className="font-medium text-fg">{resolvedTitle}</p>
          {description && <p className="break-words font-mono text-xs text-fg">{description}</p>}
          {hint && <p className="text-xs text-fg-muted">{hint}</p>}
        </div>
      </div>
      {(onRetry || action) && (
        <div className="ml-6 flex items-center gap-2">
          {onRetry && (
            <Button size="sm" variant="outline" onClick={onRetry}>
              <RefreshCw strokeWidth={1.5} aria-hidden="true" />
              {t("actions.retry", { defaultValue: "Retry" })}
            </Button>
          )}
          {action}
        </div>
      )}
    </div>
  )
}

/**
 * Inline form message: the 14px failed-status triangle plus ink text.
 * Inherits the surrounding size (13px in footers, 12px under a field);
 * renders as a span so it can sit inside a Field hint.
 */
/** Inline failure message: a 14px failed-red triangle and ink text; no red box. */
export function InlineError({
  children,
  className,
  ...props
}: { children: ReactNode; className?: string } & React.HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p role="alert" className={cn("m-0 flex items-start gap-1.5 break-words text-sm text-fg", className)} {...props}>
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
      <span className="min-w-0">{children}</span>
    </p>
  )
}

export type NoticeTone = "info" | "success" | "warning" | "error"

/**
 * Flat inline message: a 14px glyph carries the tone, the text stays in
 * ink. Replaces every tinted success / danger / warning box.
 */
export function InlineNotice({
  tone = "info",
  children,
  action,
  className,
}: {
  tone?: NoticeTone
  children: ReactNode
  action?: ReactNode
  className?: string
}) {
  return (
    <div role={tone === "error" ? "alert" : "status"} className={cn("flex items-start gap-2 text-sm text-fg", className)}>
      {tone === "success" ? (
        <StatusIcon status="completed" className="mt-0.5" />
      ) : tone === "error" ? (
        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
      ) : tone === "warning" ? (
        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-running" strokeWidth={1.5} aria-hidden="true" />
      ) : (
        <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
      )}
      <span className="min-w-0 flex-1 break-words">{children}</span>
      {action}
    </div>
  )
}
