import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { ErrorState } from "../../../components/ui/error-state"

export function AgentCloneCredentialRefresh({ failed, fetching, onRefresh }: {
  failed: boolean
  fetching: boolean
  onRefresh: () => void
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const action = <Button type="button" variant="outline" size="sm" disabled={fetching} onClick={onRefresh}>
    {fetching ? tc("states.loading") : t("agents.form.clone.refreshCredentials")}
  </Button>
  return failed
    ? <ErrorState appearance="panel" title={t("agents.form.clone.credentialsFailed")} action={action} />
    : <div>{action}</div>
}
