import { useState } from "react"
import { Check, Clock, Copy, Loader2, Mail, ShieldAlert, X } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  useRevokeInvitation,
  useUpdateInvitationRole,
  type PendingInvitation,
} from "../../lib/api-invitations"
import { ApiError } from "../../lib/api-client"
import type { MemberRole } from "../../lib/api-types"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../components/ui/alert-dialog"
import { Button } from "../../components/ui/button"
import { MemberRoleBadge } from "./MemberRoleBadge"

const INVITATION_ROLES: MemberRole[] = ["owner", "admin", "member", "viewer"]

function formatExpiration(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

export function PendingInvitationsList({
  workspaceId,
  invitations,
  inviteLinks,
  canEditRole,
}: {
  workspaceId: string
  invitations: PendingInvitation[]
  inviteLinks: Record<string, string>
  canEditRole: boolean
}) {
  const { t } = useTranslation("admin")
  const revokeInvitation = useRevokeInvitation(workspaceId)
  const updateInvitationRole = useUpdateInvitationRole(workspaceId)
  const [revokeTarget, setRevokeTarget] = useState<PendingInvitation | null>(null)
  const [copiedInvitationId, setCopiedInvitationId] = useState<string | null>(null)
  const [copyFailed, setCopyFailed] = useState(false)

  if (invitations.length === 0) return null

  const closeRevokeDialog = () => {
    if (revokeInvitation.isPending) return
    setRevokeTarget(null)
    revokeInvitation.reset()
  }

  const confirmRevoke = async () => {
    if (!revokeTarget) return
    try {
      await revokeInvitation.mutateAsync(revokeTarget.id)
      setRevokeTarget(null)
      revokeInvitation.reset()
    } catch {
      // The dialog stays open and renders the mutation error below.
    }
  }

  const handleCopy = async (invitation: PendingInvitation) => {
    const inviteLink = inviteLinks[invitation.id] ?? invitation.invite_link
    if (!inviteLink) return
    setCopyFailed(false)
    try {
      await navigator.clipboard.writeText(inviteLink)
      setCopiedInvitationId(invitation.id)
      window.setTimeout(() => setCopiedInvitationId(null), 2000)
    } catch {
      setCopyFailed(true)
    }
  }

  const handleRoleChange = async (invitation: PendingInvitation, role: MemberRole) => {
    if (role === invitation.role) return
    updateInvitationRole.reset()
    try {
      await updateInvitationRole.mutateAsync({
        invitationId: invitation.id,
        role,
      })
    } catch {
      // The list keeps the server value and renders the mutation error below.
    }
  }

  return (
    <>
      <section className="space-y-2">
        <h2 className="text-xl font-semibold text-fg">
          {t("members.invite.pendingTitle", { count: invitations.length })}
        </h2>
        <div className="divide-y divide-line overflow-hidden rounded-lg border border-line bg-surface">
          {invitations.map((invitation) => (
            <div key={invitation.id} className="flex items-center gap-3 px-4 py-3">
              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface-muted">
                <Mail className="h-4 w-4 text-fg-subtle" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium text-fg">{invitation.email}</div>
                <div className="flex items-center gap-1 text-xs text-fg-subtle">
                  <Clock className="h-3 w-3" />
                  {t("members.invite.pendingStatus")}
                  <span aria-hidden>·</span>
                  {t("members.invite.expiresAt", {
                    value: formatExpiration(invitation.expires_at),
                  })}
                </div>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="h-8 w-8 px-0"
                disabled={!inviteLinks[invitation.id] && !invitation.invite_link}
                onClick={() => void handleCopy(invitation)}
                title={
                  inviteLinks[invitation.id] || invitation.invite_link
                    ? t("members.invite.copyLink")
                    : t("members.invite.linkUnavailable")
                }
                aria-label={t("members.invite.copyLink")}
              >
                {copiedInvitationId === invitation.id ? (
                  <Check className="h-3.5 w-3.5 text-success" />
                ) : (
                  <Copy className="h-3.5 w-3.5 text-fg-subtle" />
                )}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="h-8 w-8 px-0"
                onClick={() => {
                  revokeInvitation.reset()
                  setRevokeTarget(invitation)
                }}
                title={t("members.invite.revoke.tooltip")}
                aria-label={t("members.invite.revoke.tooltip")}
              >
                <X className="h-3.5 w-3.5 text-fg-subtle" />
              </Button>
              {canEditRole ? (
                <select
                  value={invitation.role}
                  onChange={(event) =>
                    void handleRoleChange(invitation, event.target.value as MemberRole)
                  }
                  disabled={updateInvitationRole.isPending}
                  aria-label={t("members.invite.roleLabel")}
                  className="h-8 rounded-md border border-line bg-surface px-2 text-xs text-fg focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
                >
                  {INVITATION_ROLES.map((role) => (
                    <option key={role} value={role}>
                      {t(`members.role.${role}`)}
                    </option>
                  ))}
                </select>
              ) : (
                <MemberRoleBadge role={invitation.role} />
              )}
            </div>
          ))}
        </div>
        {(copyFailed || updateInvitationRole.isError) && (
          <p className="text-sm text-danger-emphasis">
            {copyFailed
              ? t("members.invite.copyError")
              : updateInvitationRole.error instanceof ApiError
                ? updateInvitationRole.error.message
                : t("members.invite.updateRoleError")}
          </p>
        )}
      </section>

      <AlertDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open) closeRevokeDialog()
        }}
      >
        <AlertDialogContent className="max-w-md gap-0 p-0">
          <AlertDialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5">
            <div className="shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis">
              <ShieldAlert className="h-4 w-4" />
            </div>
            <div className="min-w-0 space-y-1.5">
              <AlertDialogTitle className="text-sm">
                {t("members.invite.revoke.title")}
              </AlertDialogTitle>
              <AlertDialogDescription className="text-sm leading-relaxed">
                {t("members.invite.revoke.description", {
                  email: revokeTarget?.email,
                })}
              </AlertDialogDescription>
              {revokeInvitation.isError && (
                <p className="break-all text-sm text-danger-emphasis">
                  {revokeInvitation.error instanceof ApiError
                    ? revokeInvitation.error.message
                    : t("members.invite.revoke.error")}
                </p>
              )}
            </div>
          </AlertDialogHeader>
          <AlertDialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
            <AlertDialogCancel asChild>
              <Button variant="outline" size="sm" disabled={revokeInvitation.isPending}>
                {t("members.invite.revoke.cancel")}
              </Button>
            </AlertDialogCancel>
            <AlertDialogAction asChild>
              <Button
                variant="destructive"
                size="sm"
                disabled={revokeInvitation.isPending}
                onClick={(event) => {
                  event.preventDefault()
                  void confirmRevoke()
                }}
              >
                {revokeInvitation.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {t("members.invite.revoke.confirm")}
              </Button>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
