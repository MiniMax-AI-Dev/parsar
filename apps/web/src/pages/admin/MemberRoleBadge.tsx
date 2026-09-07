import { useTranslation } from "react-i18next"

import { cn } from "../../lib/utils"
import type { MemberRole } from "../../lib/api-types"

/** Role as 12px muted metadata after the name; no chip, no colour. */
export function MemberRoleBadge({ role, className }: { role: MemberRole; className?: string }) {
  const { t } = useTranslation("admin")
  return <span className={cn("shrink-0 text-xs text-fg-muted", className)}>{t(`members.role.${role}`)}</span>
}
