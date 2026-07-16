import { useTranslation } from "react-i18next"

import { Badge } from "../../components/ui/badge"
import type { MemberRole } from "../../lib/api-types"

export function MemberRoleBadge({ role }: { role: MemberRole }) {
  const { t } = useTranslation("admin")
  const variant =
    role === "owner"
      ? "primary"
      : role === "admin"
        ? "warning"
        : "neutral"

  return (
    <Badge variant={variant} dot>
      {t(`members.role.${role}`)}
    </Badge>
  )
}
