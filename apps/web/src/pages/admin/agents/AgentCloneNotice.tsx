import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { ErrorState, InlineNotice } from "../../../components/ui/error-state"
import type { AgentCapability } from "../../../lib/api-types"

export function AgentCloneNotice({ ready, failed, unavailable, onRetry, onRemove }: {
  ready: boolean
  failed: boolean
  unavailable: AgentCapability[]
  onRetry: () => void
  onRemove: (id: string) => void
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  if (failed) return <ErrorState appearance="panel" title={t("agents.form.clone.loadFailed")}
    action={<Button type="button" size="sm" variant="outline" onClick={onRetry}>{tc("actions.retry")}</Button>} />
  if (!ready) return <p role="status" className="text-sm text-fg-muted">{t("agents.form.clone.loading")}</p>
  return (
    <div className="space-y-3 rounded-md border border-line p-3">
      <InlineNotice>{t("agents.form.clone.credentials")}</InlineNotice>
      {unavailable.length > 0 && <>
        <p className="text-sm text-fg">{t("agents.form.clone.unavailable")}</p>
        {unavailable.map((binding) => (
          <div key={binding.capability_id} className="flex items-center gap-2">
            <span className="min-w-0 flex-1 break-words text-sm">{binding.capability?.name ?? binding.name ?? binding.capability_id}</span>
            <Button type="button" size="sm" variant="outline" onClick={() => onRemove(binding.capability_id)}>{t("agents.form.clone.remove")}</Button>
          </div>
        ))}
      </>}
    </div>
  )
}
