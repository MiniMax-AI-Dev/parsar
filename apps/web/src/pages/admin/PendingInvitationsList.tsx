import { Clock, Mail } from "lucide-react"
import { useTranslation } from "react-i18next"

import type { PendingInvitation } from "../../lib/api-invitations"
import { MemberRoleBadge } from "./MemberRoleBadge"

export function PendingInvitationsList({
  invitations,
}: {
  invitations: PendingInvitation[]
}) {
  const { t } = useTranslation("admin")

  if (invitations.length === 0) return null

  return (
    <section className="space-y-2">
      <h2 className="text-xl font-semibold text-fg">
        {t("members.invite.pendingTitle", { count: invitations.length })}
      </h2>
      <div className="divide-y divide-line overflow-hidden rounded-lg border border-line bg-surface">
        {invitations.map((invitation) => (
          <div
            key={invitation.id}
            className="flex items-center gap-3 px-4 py-3"
          >
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface-muted">
              <Mail className="h-4 w-4 text-fg-subtle" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium text-fg">
                {invitation.email}
              </div>
              <div className="flex items-center gap-1 text-xs text-fg-subtle">
                <Clock className="h-3 w-3" />
                {t("members.invite.pendingStatus")}
              </div>
            </div>
            <MemberRoleBadge role={invitation.role} />
          </div>
        ))}
      </div>
    </section>
  )
}
