import { useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronDown, X } from "lucide-react"

import { Button } from "../ui/button"
import { StatusIcon, type StatusKind } from "../ui/status-icon"
import type {
  ConnectivityCheck,
  ConnectivityCheckCategory,
  ConnectivityResult,
} from "../../lib/api-runtime"
import { cn } from "../../lib/utils"

interface ConnectivityResultPanelProps {
  result: ConnectivityResult
  checkLabelFor: (name: string) => string
  onDismiss: () => void
}

type SummaryKey = `runtime.connectivity.summary.${ConnectivityResult["overall"]}`
type ErrorCategoryKey = `runtime.connectivity.errorCategories.${ConnectivityCheckCategory}`
type NextStepsKey = `runtime.connectivity.nextSteps.${ConnectivityCheckCategory}`

function summaryKey(overall: ConnectivityResult["overall"]): SummaryKey {
  return `runtime.connectivity.summary.${overall}` as const
}

function errorCategoryKey(cat: ConnectivityCheckCategory): ErrorCategoryKey {
  return `runtime.connectivity.errorCategories.${cat}` as const
}

function nextStepsKey(cat: ConnectivityCheckCategory): NextStepsKey {
  return `runtime.connectivity.nextSteps.${cat}` as const
}

const STATUS_FOR_OVERALL: Record<ConnectivityResult["overall"], StatusKind> = {
  pass: "completed",
  partial: "interrupted",
  fail: "failed",
}

/** The summary copy carries a leading glyph; the status icon already says it. */
function stripLeadingGlyph(s: string): string {
  return s.replace(/^[^\p{L}\p{N}]+/u, "")
}

/**
 * Result of a connectivity test: a 32px summary row (status icon, ink
 * sentence, collapse toggle, dismiss), then one hairline row per check and
 * the raw error output in a mono `pre` on the muted tone.
 */
export function ConnectivityResultPanel({ result, checkLabelFor, onDismiss }: ConnectivityResultPanelProps) {
  const { t } = useTranslation("admin")
  // A new result (new started_at) resets the disclosure: open unless it passed.
  const resultKey = `${result.started_at}:${result.overall}`
  const [expandedFor, setExpandedFor] = useState<{ key: string; open: boolean } | null>(null)
  const expanded = expandedFor?.key === resultKey ? expandedFor.open : result.overall !== "pass"
  const setExpanded = (update: (prev: boolean) => boolean) =>
    setExpandedFor({ key: resultKey, open: update(expanded) })

  const seconds = (result.duration_ms / 1000).toFixed(1)
  const firstFail = result.checks.find((c) => !c.pass && c.error)
  const failIdx = firstFail ? result.checks.indexOf(firstFail) : -1
  const hasSkipped = failIdx >= 0 && result.checks.slice(failIdx + 1).some((c) => !c.pass && !c.error)
  const rawDetails = result.checks.filter((c) => c.error?.detail).map((c) => `${c.name}: ${c.error?.detail}`)

  return (
    <div
      className="text-sm text-fg"
      role={result.overall === "pass" ? "status" : "alert"}
      data-testid="connectivity-result-panel"
      data-overall={result.overall}
    >
      <div className="flex h-8 items-center gap-2">
        <StatusIcon status={STATUS_FOR_OVERALL[result.overall]} />
        <button
          type="button"
          onClick={() => setExpanded((prev) => !prev)}
          className="flex min-w-0 flex-1 items-center gap-1 rounded text-left font-medium text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
          aria-expanded={expanded}
          data-testid="connectivity-result-toggle"
        >
          <span className="truncate">{stripLeadingGlyph(t(summaryKey(result.overall), { seconds }))}</span>
          <ChevronDown
            className={cn("h-3.5 w-3.5 shrink-0 text-fg-muted transition-transform duration-200 ease-spring", !expanded && "-rotate-90")}
            strokeWidth={1.5}
            aria-hidden="true"
          />
        </button>
        <Button variant="ghost" size="sm" onClick={onDismiss} data-testid="connectivity-result-dismiss">
          <X strokeWidth={1.5} aria-hidden="true" />
          {t("runtime.connectivity.collapse")}
        </Button>
      </div>

      {expanded && (
        <div className="pb-2">
          <ul className="m-0 list-none p-0">
            {result.checks.map((c) => (
              <CheckRow key={c.name} check={c} label={checkLabelFor(c.name)} />
            ))}
          </ul>
          {firstFail?.error && (
            <p className="mt-2 text-sm text-fg">
              <span className="font-medium">{t("runtime.connectivity.suggestionLabel")}：</span>
              {t(nextStepsKey(normalizeCheckCategory(firstFail.error.category)))}
              {hasSkipped && <span className="text-fg-muted"> {t("runtime.connectivity.notRunAfter")}</span>}
            </p>
          )}
          {rawDetails.length > 0 && (
            <pre className="mt-2 whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
              {rawDetails.join("\n")}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

function CheckRow({ check, label }: { check: ConnectivityCheck; label: string }) {
  const { t } = useTranslation("admin")
  const seconds = (check.duration_ms / 1000).toFixed(1)
  const isSkipped = !check.pass && !check.error
  const status: StatusKind = check.pass ? "completed" : isSkipped ? "cancelled" : "failed"
  return (
    <li className="flex h-8 items-center gap-2 border-t border-line text-sm">
      <StatusIcon status={status} />
      <span className={cn("min-w-0 truncate", isSkipped ? "text-fg-muted" : "text-fg")}>
        {label}
        {check.error && (
          <span className="text-fg-muted" data-testid={`connectivity-error-${check.name}`}>
            {" · "}
            {t(errorCategoryKey(normalizeCheckCategory(check.error.category)))}
          </span>
        )}
        {isSkipped && <span className="text-fg-muted"> · {t("runtime.connectivity.checks.notRun")}</span>}
      </span>
      <span className="ml-auto shrink-0 font-mono text-xs tabular-nums text-fg-muted">{seconds}s</span>
    </li>
  )
}

const KNOWN_ERROR_CATEGORIES = new Set<ConnectivityCheckCategory>([
  "credInvalid",
  "quotaExceeded",
  "unreachable",
  "runtimeDown",
  "promptTimeout",
  "unknown",
])

function normalizeCheckCategory(category: unknown): ConnectivityCheckCategory {
  return KNOWN_ERROR_CATEGORIES.has(category as ConnectivityCheckCategory)
    ? (category as ConnectivityCheckCategory)
    : "unknown"
}
