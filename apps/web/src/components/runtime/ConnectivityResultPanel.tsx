import { useEffect, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { CheckCircle2, AlertTriangle, XCircle, ChevronDown, ChevronRight, X } from "lucide-react"

import type {
  ConnectivityCheck,
  ConnectivityCheckCategory,
  ConnectivityResult,
} from "../../lib/api-runtime"

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

export function ConnectivityResultPanel({
  result,
  checkLabelFor,
  onDismiss,
}: ConnectivityResultPanelProps) {
  const { t } = useTranslation("admin")
  const [expanded, setExpanded] = useState<boolean>(result.overall !== "pass")

  useEffect(() => {
    setExpanded(result.overall !== "pass")
  }, [result.started_at, result.overall])

  const seconds = (result.duration_ms / 1000).toFixed(1)
  const Icon = ICON_FOR_OVERALL[result.overall]
  const styles = SHAPE_FOR_OVERALL[result.overall]

  return (
    <div
      className={`mb-3 rounded-md border ${styles.container}`}
      role={result.overall === "pass" ? "status" : "alert"}
      data-testid="connectivity-result-panel"
      data-overall={result.overall}
    >
      <div className="flex items-start gap-2.5 px-3 py-2.5">
        <Icon
          className={`mt-0.5 h-4 w-4 shrink-0 ${styles.icon}`}
          strokeWidth={1.75}
        />
        <button
          type="button"
          onClick={() => setExpanded((prev) => !prev)}
          className={`flex min-w-0 flex-1 items-center gap-1 text-left text-[13px] font-medium ${styles.title} focus:outline-none focus-visible:ring-2 focus-visible:ring-slate-300`}
          aria-expanded={expanded}
          data-testid="connectivity-result-toggle"
        >
          <span className="truncate">{t(summaryKey(result.overall), { seconds })}</span>
          {expanded ? (
            <ChevronDown className="ml-1 h-3.5 w-3.5 shrink-0 opacity-60" />
          ) : (
            <ChevronRight className="ml-1 h-3.5 w-3.5 shrink-0 opacity-60" />
          )}
        </button>
        <button
          type="button"
          onClick={onDismiss}
          className={`flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[13px] font-normal ${styles.dismiss} focus:outline-none focus-visible:ring-2 focus-visible:ring-slate-300`}
          data-testid="connectivity-result-dismiss"
        >
          <X className="h-3 w-3" />
          {t("runtime.connectivity.collapse")}
        </button>
      </div>

      {expanded && (
        <div className={`border-t px-3 pb-3 pt-2.5 ${styles.detailBorder}`}>
          <ul className="space-y-1">
            {result.checks.map((c) => (
              <CheckRow key={c.name} check={c} label={checkLabelFor(c.name)} />
            ))}
          </ul>

          {result.overall !== "pass" && <FailureSuggestion checks={result.checks} />}
        </div>
      )}
    </div>
  )
}

function CheckRow({
  check,
  label,
}: {
  check: ConnectivityCheck
  label: string
}) {
  const { t } = useTranslation("admin")
  const seconds = (check.duration_ms / 1000).toFixed(1)
  const Icon = check.pass ? CheckCircle2 : XCircle
  const iconClr = check.pass ? "text-emerald-600" : "text-red-600"
  const isSkipped = !check.pass && !check.error
  return (
    <li className="flex items-start gap-2 text-[13px]">
      {isSkipped ? (
        <span className="mt-0.5 h-3.5 w-3.5 shrink-0 rounded-full bg-slate-200" aria-hidden />
      ) : (
        <Icon className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${iconClr}`} strokeWidth={2} />
      )}
      <span className={isSkipped ? "text-slate-400" : "text-slate-700"}>
        {label}
      </span>
      <span className="text-slate-400">{seconds}s</span>
      {check.error && (
        <span
          className="text-red-700"
          title={check.error.detail ?? ""}
          data-testid={`connectivity-error-${check.name}`}
        >
          — {t(errorCategoryKey(normalizeCheckCategory(check.error.category)))}
        </span>
      )}
      {isSkipped && (
        <span className="text-slate-400">
          — {t("runtime.connectivity.checks.notRun")}
        </span>
      )}
    </li>
  )
}

function FailureSuggestion({ checks }: { checks: ConnectivityCheck[] }): ReactNode {
  const { t } = useTranslation("admin")
  const firstFail = checks.find((c) => !c.pass && c.error)
  if (!firstFail || !firstFail.error) return null
  const category = normalizeCheckCategory(firstFail.error.category)
  const failIdx = checks.findIndex((c) => c === firstFail)
  const hasSkipped = checks.slice(failIdx + 1).some((c) => !c.pass && !c.error)
  return (
    <div className="mt-2 rounded-md bg-slate-50/70 px-2.5 py-2 text-[13px] text-slate-700">
      <span className="font-medium text-slate-800">
        {t("runtime.connectivity.suggestionLabel")}：
      </span>
      <span className="ml-1">{t(nextStepsKey(category))}</span>
      {hasSkipped && (
        <p className="mt-1 text-slate-500">{t("runtime.connectivity.notRunAfter")}</p>
      )}
    </div>
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

const ICON_FOR_OVERALL: Record<ConnectivityResult["overall"], typeof CheckCircle2> = {
  pass: CheckCircle2,
  partial: AlertTriangle,
  fail: XCircle,
}

const SHAPE_FOR_OVERALL: Record<
  ConnectivityResult["overall"],
  { container: string; icon: string; title: string; dismiss: string; detailBorder: string }
> = {
  pass: {
    container: "border-emerald-200 bg-emerald-50/60",
    icon: "text-emerald-600",
    title: "text-emerald-900",
    dismiss: "text-emerald-700 hover:bg-emerald-100/60",
    detailBorder: "border-emerald-200",
  },
  partial: {
    container: "border-amber-200 bg-amber-50/70",
    icon: "text-amber-600",
    title: "text-amber-900",
    dismiss: "text-amber-700 hover:bg-amber-100/60",
    detailBorder: "border-amber-200",
  },
  fail: {
    container: "border-red-200 bg-red-50/60",
    icon: "text-red-600",
    title: "text-red-900",
    dismiss: "text-red-700 hover:bg-red-100/60",
    detailBorder: "border-red-200",
  },
}
