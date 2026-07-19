import { useTranslation } from "react-i18next"

import { Badge } from "../../../components/ui/badge"
import type { Agent } from "../../../lib/api-types"

export function AgentStatusBadge({ status }: { status: Agent["status"] }) {
  const { t } = useTranslation("admin")
  if (status === "active")
    return (
      <Badge variant="success" dot>
        {t("agents.status.active")}
      </Badge>
    )
  if (status === "error")
    return (
      <Badge variant="destructive" dot>
        {t("agents.status.error")}
      </Badge>
    )
  return <Badge variant="neutral">{t("agents.status.disabled")}</Badge>
}
