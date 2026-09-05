import { useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { AlertTriangle, Check, Copy, Loader2, ShieldCheck, X } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  useRevokeInvitation,
  useUpdateInvitationRole,
  type PendingInvitation,
} from "../../lib/api-invitations"
import { ApiError } from "../../lib/api-client"
import type { MemberRole } from "../../lib/api-types"
import { useRelativeTime } from "../../lib/relative-time"
import { ActionIconButton, RowActions } from "../../components/ui/action-button"
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
import { InitialTile, LedgerGroup, LedgerId, LedgerRow } from "../../components/ui/ledger"
import { MemberRoleBadge } from "./MemberRoleBadge"

const INVITATION_ROLES: MemberRole[] = ["owner", "admin", "member", "viewer"]

function formatExpiration(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

/**
 * The pending-invitations group of the members ledger. Rows share the
 * parent Ledger's column template: invitee · expiry · role · age · actions.
 */
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
  const fmtAgo = useRelativeTime()
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

  const inlineError = copyFailed
    ? t("members.invite.copyError")
    : updateInvitationRole.isError
      ? updateInvitationRole.error instanceof ApiError
        ? updateInvitationRole.error.message
        : t("members.invite.updateRoleError")
      : null

  return (
    <>
      <LedgerGroup label={t("members.invite.pendingLabel")} count={invitations.length}>
        {inlineError && (
          <li className="flex h-9 items-center gap-1.5 border-b border-line px-4 text-sm text-fg">
            <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            <span className="truncate">{inlineError}</span>
          </li>
        )}
        {invitations.map((invitation) => {
          const link = inviteLinks[invitation.id] || invitation.invite_link
          return (
            <LedgerRow key={invitation.id}>
              <span className="flex min-w-0 items-center gap-1.5">
                <InitialTile name={invitation.email} />
                <span className="truncate font-medium">{invitation.email}</span>
              </span>
              <LedgerId>
                {t("members.invite.expiresAt", { value: formatExpiration(invitation.expires_at) })}
              </LedgerId>
              <span className="flex items-center">
                <MemberRoleBadge role={invitation.role} />
              </span>
              <span className="truncate text-right text-xs text-fg-muted">{fmtAgo(invitation.created_at)}</span>
              <RowActions>
                <ActionIconButton
                  icon={copiedInvitationId === invitation.id ? Check : Copy}
                  label={link ? t("members.invite.copyLink") : t("members.invite.linkUnavailable")}
                  disabled={!link}
                  onClick={() => void handleCopy(invitation)}
                />
                {canEditRole && (
                  <DropdownMenu.Root>
                    <DropdownMenu.Trigger asChild>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("members.invite.roleLabel")}
                        title={t("members.invite.roleLabel")}
                        disabled={updateInvitationRole.isPending}
                        onClick={(event) => event.stopPropagation()}
                      >
                        <ShieldCheck strokeWidth={1.5} aria-hidden="true" />
                      </Button>
                    </DropdownMenu.Trigger>
                    <DropdownMenu.Portal>
                      <DropdownMenu.Content
                        align="end"
                        sideOffset={6}
                        className="app-shadow-floating z-50 min-w-[160px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in"
                      >
                        <DropdownMenu.RadioGroup
                          value={invitation.role}
                          onValueChange={(value) => void handleRoleChange(invitation, value as MemberRole)}
                        >
                          {INVITATION_ROLES.map((role) => (
                            <DropdownMenu.RadioItem
                              key={role}
                              value={role}
                              className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"
                            >
                              <span className="flex-1">{t(`members.role.${role}`)}</span>
                              <DropdownMenu.ItemIndicator>
                                <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
                              </DropdownMenu.ItemIndicator>
                            </DropdownMenu.RadioItem>
                          ))}
                        </DropdownMenu.RadioGroup>
                      </DropdownMenu.Content>
                    </DropdownMenu.Portal>
                  </DropdownMenu.Root>
                )}
                <ActionIconButton
                  icon={X}
                  label={t("members.invite.revoke.tooltip")}
                  tone="danger"
                  onClick={() => {
                    revokeInvitation.reset()
                    setRevokeTarget(invitation)
                  }}
                />
              </RowActions>
            </LedgerRow>
          )
        })}
      </LedgerGroup>

      <AlertDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open) closeRevokeDialog()
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("members.invite.revoke.title")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("members.invite.revoke.description", { email: revokeTarget?.email })}
            </AlertDialogDescription>
            {revokeInvitation.isError && (
              <p className="flex items-start gap-1.5 break-all text-sm text-fg">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
                <span>
                  {revokeInvitation.error instanceof ApiError
                    ? revokeInvitation.error.message
                    : t("members.invite.revoke.error")}
                </span>
              </p>
            )}
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button variant="outline" disabled={revokeInvitation.isPending}>
                {t("members.invite.revoke.cancel")}
              </Button>
            </AlertDialogCancel>
            <AlertDialogAction asChild>
              <Button
                variant="destructive"
                disabled={revokeInvitation.isPending}
                onClick={(event) => {
                  event.preventDefault()
                  void confirmRevoke()
                }}
              >
                {revokeInvitation.isPending && <Loader2 className="animate-spin" />}
                {t("members.invite.revoke.confirm")}
              </Button>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
