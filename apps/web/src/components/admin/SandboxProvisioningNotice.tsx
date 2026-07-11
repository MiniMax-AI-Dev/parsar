import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import { Button } from "../ui/button"
import type { Runtime } from "../../lib/api-runtimes"
import { useNow } from "../../lib/use-now"

type StepState = "active" | "pending"

function Detail({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="mb-0.5 text-xs uppercase tracking-wider text-fg-faint">{label}</dt>
      <dd className={`text-sm text-fg-emphasis ${mono ? "font-mono break-all" : ""}`}>{value}</dd>
    </div>
  )
}

function elapsedSeconds(startedAt?: string): number {
  if (!startedAt) return 0
  const started = new Date(startedAt).getTime()
  if (Number.isNaN(started)) return 0
  return Math.max(0, Math.floor((Date.now() - started) / 1000))
}

function Step({ state, label, detail }: { state: StepState; label: string; detail?: string }) {
  return (
    <li className="grid grid-cols-[1rem_1fr] gap-2">
      <span
        className={
          state === "active"
            ? "mt-1 h-2 w-2 rounded-full bg-warning-emphasis"
            : "mt-1 h-2 w-2 rounded-full border border-warning-border bg-surface"
        }
      />
      <span>
        <span className="block text-sm font-medium text-warning-emphasis">{label}</span>
        {detail && (
          <span className="block text-sm leading-relaxed text-warning-emphasis">{detail}</span>
        )}
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
    <div className="rounded-md border border-warning-border bg-warning-subtle px-3 py-2">
      <div className="flex items-center gap-2 text-sm font-medium text-warning-emphasis">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        {t("agents.detail.sandbox.preparing.title")}
      </div>
      <p className="mt-1 text-sm leading-relaxed text-warning-emphasis">
        {t("agents.detail.sandbox.preparing.body")}
      </p>
      <p className="mt-1 text-sm leading-relaxed text-warning-emphasis">
        {t("agents.detail.sandbox.preparing.coldStartBody")}
      </p>
      <ol className="mt-3 space-y-2">
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
      <dl className="mt-2 space-y-1">
        {runtime && (
          <Detail label={t("agents.detail.sandbox.preparing.runtimeId")} value={runtime.id} mono />
        )}
        {startedAt && (
          <Detail
            label={t("agents.detail.sandbox.preparing.started")}
            value={new Date(startedAt).toLocaleString()}
          />
        )}
      </dl>
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
    <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2">
      <div className="text-sm font-medium text-danger-emphasis">
        {t("agents.detail.sandbox.startupTimedOut.title")}
      </div>
      <p className="mt-1 text-sm leading-relaxed text-danger-emphasis">
        {t("agents.detail.sandbox.startupTimedOut.body")}
      </p>
      <dl className="mt-2">
        <Detail label={t("agents.detail.sandbox.preparing.runtimeId")} value={runtime.id} mono />
      </dl>
      <Button size="sm" variant="outline" className="mt-3" disabled={retrying} onClick={onRetry}>
        {retrying && <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" />}
        {t("agents.detail.sandbox.actions.retryProvision")}
      </Button>
    </div>
  )
}
