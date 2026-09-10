import { useState } from "react"
import { ArrowUpRight, RefreshCw } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "../../../components/ui/button"
import { InlineError } from "../../../components/ui/error-state"
import { useModels } from "../../../lib/api-models"

export function AgentModelPrerequisite({ workspaceID }: { workspaceID: string | null }) {
  const { t } = useTranslation("admin")
  const modelsQ = useModels(workspaceID)
  const [refreshAttempted, setRefreshAttempted] = useState(false)
  const setupURL = `/?${new URLSearchParams({ admin: "models", ...(workspaceID ? { ws: workspaceID } : {}) })}`

  return (
    <div className="flex flex-col items-start gap-1 py-1">
      <p className="text-sm font-medium text-fg">{t("agents.form.emptyModel.title")}</p>
      <p className="text-xs text-fg-muted">{t("agents.form.emptyModel.description")}</p>
      <div className="mt-2 flex flex-wrap gap-2">
        <Button variant="outline" size="sm" asChild>
          <a href={setupURL} target="_blank" rel="noreferrer">
            {t("agents.form.emptyModel.cta")}
            <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
          </a>
        </Button>
        <Button variant="outline" size="sm" disabled={modelsQ.isFetching} onClick={() => {
          setRefreshAttempted(true)
          void modelsQ.refetch()
        }}>
          <RefreshCw strokeWidth={1.5} aria-hidden="true" />
          {t("agents.form.emptyModel.refresh")}
        </Button>
      </div>
      {refreshAttempted && modelsQ.isError && (
        <InlineError className="mt-1">{t("agents.form.emptyModel.refreshError")}</InlineError>
      )}
    </div>
  )
}
