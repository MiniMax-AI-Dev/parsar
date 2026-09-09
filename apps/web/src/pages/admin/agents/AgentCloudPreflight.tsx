import { ArrowUpRight, RefreshCw } from "lucide-react"
import { useTranslation } from "react-i18next"

import { RuntimeStatusBanner } from "../../../components/runtime/RuntimeStatusBanner"
import { Button } from "../../../components/ui/button"

export function AgentCloudPreflight({ workspaceID, checking, failed, onRetry }: {
  workspaceID: string
  checking: boolean
  failed: boolean
  onRetry: () => void
}) {
  const { t } = useTranslation("admin")
  const setupURL = `/?${new URLSearchParams({ ws: workspaceID, admin: "runtime", tab: "instances" })}`

  return (
    <section className="shrink-0 rounded-md border border-line bg-surface p-3">
      <RuntimeStatusBanner workspaceID={workspaceID} />
      <p className="mt-2 text-xs text-fg-muted">{t("agents.form.cloudPreflight.hint")}</p>
      <div className="mt-3 flex flex-wrap gap-2">
        <Button asChild variant="outline" size="sm">
          <a href={setupURL} target="_blank" rel="noreferrer">
            {t("agents.form.cloudPreflight.configure")}
            <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
          </a>
        </Button>
        {!failed && (
          <Button variant="outline" size="sm" disabled={checking} onClick={onRetry}>
            <RefreshCw strokeWidth={1.5} aria-hidden="true" />
            {t("agents.form.cloudPreflight.retry")}
          </Button>
        )}
      </div>
    </section>
  )
}
