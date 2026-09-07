import { useTranslation } from "react-i18next"
import { AlertTriangle, Loader2 } from "lucide-react"

import { Button } from "../ui/button"
import { PropertyList, Property } from "../ui/property-list"
import { StatusIcon } from "../ui/status-icon"
import type { Runtime } from "../../lib/api-runtimes"
import { useNow } from "../../lib/use-now"

type StepState = "active" | "pending"

function elapsedSeconds(startedAt?: string): number {
  if (!startedAt) return 0
  const started = new Date(startedAt).getTime()
  if (Number.isNaN(started)) return 0
  return Math.max(0, Math.floor((Date.now() - started) / 1000))
}

/** One 32px hairline step row: status icon, title in ink, detail muted after " · ". */
function Step({ state, label, detail }: { state: StepState; label: string; detail?: string }) {
  return (
    <li className="flex h-8 items-center gap-2 border-b border-line text-sm last:border-b-0">
      <StatusIcon status={state === "active" ? "running" : "queued"} />
      <span className="min-w-0 truncate text-fg" title={detail ? `${label} · ${detail}` : label}>
        {label}
        {detail && <span className="text-fg-muted"> · {detail}</span>}
      </span>
    </li>
  )
}

export function SandboxPreparingNotice({
  runtime,
  startedAt,
}: {
  runtime?: Runtime | null
  startedAt?: string
}) {
  const { t } = useTranslation("admin")
  useNow()
  const elapsed = elapsedSeconds(startedAt)
  const imagePullActive = elapsed >= 10
  const slowImagePull = elapsed >= 30
  return (
    <div role="status">
      <p className="flex h-8 items-center gap-2 text-sm font-medium text-fg">
        <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-fg-muted motion-reduce:animate-none" strokeWidth={1.5} aria-hidden="true" />
        {t("agents.detail.sandbox.preparing.title")}
      </p>
      <ol className="m-0 list-none border-t border-line p-0">
        <Step
          state={imagePullActive ? "pending" : "active"}
          label={t("agents.detail.sandbox.preparing.steps.prepareImage")}
          detail={t("agents.detail.sandbox.preparing.steps.prepareImageDetail")}
        />
        <Step
          state={imagePullActive ? "active" : "pending"}
          label={t("agents.detail.sandbox.preparing.steps.pullImage")}
          detail={
            slowImagePull
              ? t("agents.detail.sandbox.preparing.steps.pullImageSlowDetail")
              : t("agents.detail.sandbox.preparing.steps.pullImageDetail")
          }
        />
        <Step
          state="pending"
          label={t("agents.detail.sandbox.preparing.steps.startContainer")}
          detail={t("agents.detail.sandbox.preparing.steps.startContainerDetail")}
        />
        <Step
          state="pending"
          label={t("agents.detail.sandbox.preparing.steps.pairDaemon")}
          detail={t("agents.detail.sandbox.preparing.steps.pairDaemonDetail")}
        />
      </ol>
      {(runtime || startedAt) && (
        <PropertyList className="mt-2">
          {runtime && (
            <Property label={t("agents.detail.sandbox.preparing.runtimeId")} mono>{runtime.id}</Property>
          )}
          {startedAt && (
            <Property label={t("agents.detail.sandbox.preparing.started")} mono>
              {new Date(startedAt).toLocaleString()}
            </Property>
          )}
        </PropertyList>
      )}
    </div>
  )
}

export function SandboxStartupTimedOutNotice({
  runtime,
  retrying,
  onRetry,
}: {
  runtime: Runtime
  retrying: boolean
  onRetry: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div role="alert" className="text-sm">
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
        <div className="min-w-0">
          <p className="font-medium text-fg">{t("agents.detail.sandbox.startupTimedOut.title")}</p>
          <p className="text-xs text-fg-muted">{t("agents.detail.sandbox.startupTimedOut.body")}</p>
        </div>
      </div>
      <PropertyList className="mt-2">
        <Property label={t("agents.detail.sandbox.preparing.runtimeId")} mono>{runtime.id}</Property>
      </PropertyList>
      <Button size="sm" variant="outline" className="mt-2" disabled={retrying} onClick={onRetry}>
        {retrying && <Loader2 className="animate-spin" />}
        {t("agents.detail.sandbox.actions.retryProvision")}
      </Button>
    </div>
  )
}
