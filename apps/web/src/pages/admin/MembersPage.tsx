import { useState } from "react"
import { useTranslation } from "react-i18next"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import {
  AlertTriangle,
  Check,
  Copy,
  Loader2,
  ShieldCheck,
  UserMinus,
  UserPlus,
  Users,
  X,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { UserSearchCombobox } from "../../components/UserSearchCombobox"
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
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Field } from "../../components/ui/label"
import {
  InitialTile,
  Ledger,
  LedgerGroup,
  LedgerHeader,
  LedgerId,
  LedgerRow,
  col,
} from "../../components/ui/ledger"
import { Select } from "../../components/ui/select"
import { Skeleton } from "../../components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import {
  useAddWorkspaceMember,
  useRemoveWorkspaceMember,
  useUpdateWorkspaceMemberRole,
  useWorkspaceMembers,
} from "../../lib/api-members"
import {
  useCreateInvitation,
  usePendingInvitations,
} from "../../lib/api-invitations"
import type {
  AddWorkspaceMemberRequest,
  MemberRole,
  PendingJoinRequest,
  PlatformUser,
  WorkspaceMember,
} from "../../lib/api-types"
import {
  useApproveJoinRequest,
  useMyWorkspaces,
  usePendingJoinRequests,
  useRejectJoinRequest,
} from "../../lib/api-workspaces"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"
import { MemberRoleBadge } from "./MemberRoleBadge"
import { PendingInvitationsList } from "./PendingInvitationsList"

const ROLES: MemberRole[] = ["owner", "admin", "member", "viewer"]

/** user · email / detail · role · age · actions */
const LEDGER_COLUMNS = [col.title(), col.id(200, 1), col.meta(104), col.age(80), col.actions(2)]

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export function MembersPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const [inviteOpen, setInviteOpen] = useState(false)
  const [invitePermissionOpen, setInvitePermissionOpen] = useState(false)
  const [inviteLinks, setInviteLinks] = useState<Record<string, string>>({})
  const [removeWsTarget, setRemoveWsTarget] = useState<WorkspaceMember | null>(null)

  const myWorkspacesQ = useMyWorkspaces()
  const wsQ = useWorkspaceMembers(wsId)
  const workspaceRole = myWorkspacesQ.data?.workspaces.find((workspace) => workspace.id === wsId)?.role
  const canManageInvitations = workspaceRole === "owner" || workspaceRole === "admin"
  const canInviteMembers = canManageInvitations || workspaceRole === "member"
  const invitationsQ = usePendingInvitations(canInviteMembers ? wsId : null)
  const joinRequestsQ = usePendingJoinRequests(canManageInvitations ? wsId : null)
  const addWsMut = useAddWorkspaceMember(wsId)
  const updateWsRoleMut = useUpdateWorkspaceMemberRole(wsId)
  const removeWsMut = useRemoveWorkspaceMember(wsId)

  const members = wsQ.data?.members ?? []
  const invitations = invitationsQ.data ?? []
  const joinRequests = joinRequestsQ.data?.requests ?? []

  const handleWsRoleChange = async (m: WorkspaceMember, role: MemberRole) => {
    if (role === m.role) return
    try {
      await updateWsRoleMut.mutateAsync({ userId: m.user_id, role })
    } catch {
      void wsQ.refetch()
    }
  }

  const pageTitle = t("members.page.title")
  const loadError = wsQ.isError ? (wsQ.error as ApiError) : undefined
  const mutationError =
    (updateWsRoleMut.error as ApiError | null)?.message ??
    (invitationsQ.isError ? (invitationsQ.error as ApiError).message : null)

  return (
    <AdminLayout activeMenu="members" fullBleed>
      <div className="flex min-w-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={pageTitle}
          subtitleFor="members.page.title"
          action={
            <Button
              onClick={() => {
                if (!canInviteMembers) {
                  setInvitePermissionOpen(true)
                  return
                }
                setInviteOpen(true)
              }}
              disabled={!wsId || myWorkspacesQ.isLoading}
            >
              <UserPlus strokeWidth={1.5} aria-hidden="true" />
              {t("members.invite.cta")}
            </Button>
          }
        />

        {!wsId ? (
          <div className="px-6"><ScopeRequiredState scope="workspace" resourceName={pageTitle} /></div>
        ) : wsQ.isLoading ? (
          <MembersLoadingSkeleton />
        ) : loadError ? (
          <div className="px-6 pt-6">
            <ErrorState
              title={loadError.envelope?.unreachable ? t("members.error.unreachable.title") : t("members.error.load.title")}
              description={
                loadError.envelope?.unreachable
                  ? t("members.error.unreachable.description")
                  : loadError.message ?? t("members.error.load.description")
              }
              hint={loadError.envelope?.unreachable ? t("members.error.unreachable.hint") : t("members.error.load.hint")}
              onRetry={() => void wsQ.refetch()}
            />
          </div>
        ) : members.length === 0 && invitations.length === 0 && joinRequests.length === 0 ? (
          <EmptyState icon={Users} title={t("members.empty.ws.title")} description={t("members.empty.ws.description")} />
        ) : (
          <Ledger columns={LEDGER_COLUMNS} role="listbox" aria-label={pageTitle}>
            <LedgerHeader>
              <span>{t("members.table.user")}</span>
              <span>{t("members.invite.emailLabel")}</span>
              <span>{t("members.table.role")}</span>
              <span className="text-right">{t("members.table.joinedAt")}</span>
              <span />
            </LedgerHeader>

            {mutationError && (
              <p className="flex h-9 items-center gap-1.5 border-b border-line px-4 text-sm text-fg">
                <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
                <span className="truncate">{mutationError}</span>
              </p>
            )}

            {members.length > 0 && (
              <LedgerGroup label={t("members.tabs.workspace")} count={members.length}>
                {members.map((m) => (
                  <MemberRow
                    key={m.id}
                    member={m}
                    writable={canManageInvitations && m.role !== "owner"}
                    roleChangePending={updateWsRoleMut.isPending}
                    onChangeRole={(role) => void handleWsRoleChange(m, role)}
                    onRemove={() => setRemoveWsTarget(m)}
                  />
                ))}
              </LedgerGroup>
            )}

            {canInviteMembers && (
              <PendingInvitationsList
                workspaceId={wsId}
                invitations={invitations}
                inviteLinks={inviteLinks}
                canEditRole={canManageInvitations}
              />
            )}

            {canManageInvitations && joinRequests.length > 0 && (
              <PendingJoinRequestsGroup wsId={wsId} requests={joinRequests} />
            )}
          </Ledger>
        )}
      </div>

      {inviteOpen && wsId && (
        <InviteMemberDialog
          wsId={wsId}
          canManage={canManageInvitations}
          onClose={() => {
            setInviteOpen(false)
            addWsMut.reset()
          }}
          onCreated={(invitationId, inviteLink) => {
            setInviteLinks((current) => ({ ...current, [invitationId]: inviteLink }))
          }}
          addOne={(body) => addWsMut.mutateAsync(body)}
        />
      )}

      <Dialog open={invitePermissionOpen} onOpenChange={setInvitePermissionOpen}>
        <DialogContent showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>{t("members.invite.permission.title")}</DialogTitle>
            <DialogDescription>{t("members.invite.permission.description")}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setInvitePermissionOpen(false)}>
              {t("members.invite.close")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {removeWsTarget && (
        <ConfirmRemoveDialog
          targetLabel={removeWsTarget.user_name || removeWsTarget.user_email}
          pending={removeWsMut.isPending}
          error={removeWsMut.error as ApiError | undefined}
          onCancel={() => {
            setRemoveWsTarget(null)
            removeWsMut.reset()
          }}
          onConfirm={async () => {
            try {
              await removeWsMut.mutateAsync(removeWsTarget.user_id)
              setRemoveWsTarget(null)
              removeWsMut.reset()
            } catch {
              // surfaces via prop
            }
          }}
        />
      )}
    </AdminLayout>
  )
}

/* ------------------------------------------------------------------ */
/*  Rows                                                               */
/* ------------------------------------------------------------------ */

function MemberRow({
  member: m,
  writable,
  roleChangePending,
  onChangeRole,
  onRemove,
}: {
  member: WorkspaceMember
  writable: boolean
  roleChangePending: boolean
  onChangeRole: (role: MemberRole) => void
  onRemove: () => void
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const name = m.user_name || m.user_email
  return (
    <LedgerRow>
      <span className="flex min-w-0 items-center gap-1.5">
        <InitialTile name={name} />
        <span className="truncate font-medium">{name}</span>
        {m.user_status === "disabled" && (
          <Badge variant="neutral" dot className="shrink-0">{t("members.userStatus.disabled")}</Badge>
        )}
      </span>
      <LedgerId>{m.user_email}</LedgerId>
      <span className="flex items-center">
        <MemberRoleBadge role={m.role} />
      </span>
      <span className="truncate text-right text-xs text-fg-muted">{fmtAgo(m.created_at)}</span>
      {writable ? (
        <RowActions>
          <RoleMenu value={m.role} disabled={roleChangePending} onChange={onChangeRole} />
          <ActionIconButton icon={UserMinus} label={t("members.remove.cta")} tone="danger" onClick={onRemove} />
        </RowActions>
      ) : (
        <span />
      )}
    </LedgerRow>
  )
}

/** Change-role menu: a ghost icon trigger with the roles as radio items. */
function RoleMenu({
  value,
  disabled,
  onChange,
}: {
  value: MemberRole
  disabled: boolean
  onChange: (role: MemberRole) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("members.table.role")}
          title={t("members.table.role")}
          disabled={disabled}
          onClick={(event) => event.stopPropagation()}
        >
          <ShieldCheck strokeWidth={1.5} aria-hidden="true" />
        </Button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={6}
          className="app-shadow-floating z-50 min-w-[160px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in data-[state=closed]:animate-pop-out"
        >
          <DropdownMenu.RadioGroup value={value} onValueChange={(next) => onChange(next as MemberRole)}>
            {ROLES.filter((r) => r !== "owner").map((r) => (
              <DropdownMenu.RadioItem
                key={r}
                value={r}
                className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"
              >
                <span className="flex-1">{t(`members.role.${r}`)}</span>
                <DropdownMenu.ItemIndicator>
                  <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
                </DropdownMenu.ItemIndicator>
              </DropdownMenu.RadioItem>
            ))}
          </DropdownMenu.RadioGroup>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

function MembersLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-[18px] w-[18px] rounded" />
          <Skeleton className="h-3 w-32" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Pending join requests group                                        */
/*                                                                     */
/*  User-initiated "join workspace" requests. The server stores them as   */
/*  workspace_members rows with status='pending'; the UI lists them as a  */
/*  group of the same ledger with approve / reject row actions.          */
/* ------------------------------------------------------------------ */

function PendingJoinRequestsGroup({ wsId, requests }: { wsId: string; requests: PendingJoinRequest[] }) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const fmtAgo = useRelativeTime()
  const approveMut = useApproveJoinRequest()
  const rejectMut = useRejectJoinRequest()
  const [confirmReject, setConfirmReject] = useState<PendingJoinRequest | null>(null)
  const busy = approveMut.isPending || rejectMut.isPending

  return (
    <>
      <LedgerGroup label={t("members.tabs.pending")} count={requests.length}>
        {requests.map((req) => {
          const name = req.user_name || req.user_email
          return (
            <LedgerRow key={req.id}>
              <span className="flex min-w-0 items-center gap-1.5">
                <InitialTile name={name} />
                <span className="shrink-0 truncate font-medium">{name}</span>
                <span className="min-w-0 truncate text-xs text-fg-muted" title={req.request_reason || undefined}>
                  · {req.request_reason || t("members.pendingRequests.noReason")}
                </span>
              </span>
              <LedgerId>{req.user_email}</LedgerId>
              <span className="text-fg-muted">—</span>
              <span className="truncate text-right text-xs text-fg-muted">{fmtAgo(req.requested_at)}</span>
              <RowActions>
                <ActionIconButton
                  icon={Check}
                  label={t("members.pendingRequests.actions.approve")}
                  busy={approveMut.isPending && approveMut.variables?.requestId === req.id}
                  disabled={busy}
                  onClick={() => approveMut.mutate({ wsId, requestId: req.id, request: req })}
                />
                <ActionIconButton
                  icon={X}
                  label={t("members.pendingRequests.actions.reject")}
                  tone="danger"
                  disabled={busy}
                  onClick={() => setConfirmReject(req)}
                />
              </RowActions>
            </LedgerRow>
          )
        })}
      </LedgerGroup>

      {/* Rejecting is not destructive (the applicant can re-apply), so the
          confirm is a primary action, not the red one. */}
      <AlertDialog
        open={confirmReject !== null}
        onOpenChange={(open) => {
          if (!open) {
            setConfirmReject(null)
            rejectMut.reset()
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("members.pendingRequests.confirmRejectTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("members.pendingRequests.confirmRejectBody", {
                name: confirmReject?.user_name || confirmReject?.user_email || "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button variant="outline" disabled={rejectMut.isPending}>{tc("actions.cancel")}</Button>
            </AlertDialogCancel>
            <AlertDialogAction asChild>
              <Button
                disabled={rejectMut.isPending}
                onClick={(event) => {
                  event.preventDefault()
                  if (!confirmReject) return
                  rejectMut.mutate(
                    { wsId, requestId: confirmReject.id, request: confirmReject },
                    { onSuccess: () => setConfirmReject(null) },
                  )
                }}
              >
                {rejectMut.isPending && <Loader2 className="animate-spin" />}
                {t("members.pendingRequests.actions.reject")}
              </Button>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

/* ------------------------------------------------------------------ */
/*  Invite dialog                                                      */
/*                                                                     */
/*  One entry point for growing the team. Owners and admins get two     */
/*  segments: an invitation link for someone new, or picking existing   */
/*  platform users; members only get the link form.                     */
/* ------------------------------------------------------------------ */

type InviteMode = "link" | "existing"

interface InviteResult {
  email: string
  inviteLink: string
}

function InviteMemberDialog({
  wsId,
  canManage,
  onClose,
  onCreated,
  addOne,
}: {
  wsId: string
  canManage: boolean
  onClose: () => void
  onCreated: (invitationId: string, inviteLink: string) => void
  addOne: (body: AddWorkspaceMemberRequest) => Promise<unknown>
}) {
  const { t } = useTranslation("admin")
  const [mode, setMode] = useState<InviteMode>("link")
  const [pending, setPending] = useState(false)
  const [result, setResult] = useState<InviteResult | null>(null)

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next && !pending) onClose()
      }}
    >
      <DialogContent>
        {result ? (
          <InviteResultView result={result} onClose={onClose} />
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t("members.invite.title")}</DialogTitle>
            </DialogHeader>
            {canManage && (
              <Tabs value={mode} onValueChange={(v) => setMode(v as InviteMode)}>
                <TabsList className="flex w-full">
                  <TabsTrigger value="link" className="flex-1" disabled={pending}>{t("members.invite.tabs.link")}</TabsTrigger>
                  <TabsTrigger value="existing" className="flex-1" disabled={pending}>{t("members.invite.tabs.existing")}</TabsTrigger>
                </TabsList>
              </Tabs>
            )}
            {mode === "link" || !canManage ? (
              <InviteLinkForm
                wsId={wsId}
                canChooseRole={canManage}
                onPendingChange={setPending}
                onClose={onClose}
                onCreated={(id, link, email) => {
                  onCreated(id, link)
                  setResult({ email, inviteLink: link })
                }}
              />
            ) : (
              <AddExistingForm wsId={wsId} onPendingChange={setPending} onClose={onClose} addOne={addOne} />
            )}
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

function InviteLinkForm({
  wsId,
  canChooseRole,
  onPendingChange,
  onClose,
  onCreated,
}: {
  wsId: string
  canChooseRole: boolean
  onPendingChange: (pending: boolean) => void
  onClose: () => void
  onCreated: (invitationId: string, inviteLink: string, email: string) => void
}) {
  const { t } = useTranslation("admin")
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [role, setRole] = useState<MemberRole>("member")
  const [pending, setPending] = useState(false)
  const [errMsg, setErrMsg] = useState<string | null>(null)
  const createInvitation = useCreateInvitation(wsId)

  const canSubmit = email.trim() !== "" && !pending

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!canSubmit) return
    setErrMsg(null)
    setPending(true)
    onPendingChange(true)
    try {
      const res = await createInvitation.mutateAsync({
        email: email.trim(),
        name: name.trim() || undefined,
        role: canChooseRole ? role : "member",
      })
      onCreated(res.invitation_id, res.invite_link, res.email)
    } catch (err) {
      setErrMsg(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err))
    } finally {
      setPending(false)
      onPendingChange(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div className="flex flex-col gap-3">
        <Field label={t("members.invite.emailLabel")} htmlFor="invite-email">
          <Input
            id="invite-email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder={t("members.invite.emailPlaceholder")}
            autoComplete="off"
            required
            disabled={pending}
          />
        </Field>
        <Field label={t("members.invite.nameLabel")} htmlFor="invite-name">
          <Input
            id="invite-name"
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("members.invite.namePlaceholder")}
            autoComplete="off"
            disabled={pending}
          />
        </Field>
        <Field label={t("members.invite.roleLabel")} htmlFor="invite-role">
          {canChooseRole ? (
            <Select id="invite-role" value={role} onChange={(e) => setRole(e.target.value as MemberRole)} disabled={pending}>
              {ROLES.map((r) => (
                <option key={r} value={r}>{t(`members.role.${r}`)}</option>
              ))}
            </Select>
          ) : (
            <span className="flex h-7 items-center"><MemberRoleBadge role="member" /></span>
          )}
        </Field>
        {errMsg && <InlineError message={errMsg} />}
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose} disabled={pending}>
          {t("members.invite.cancel")}
        </Button>
        <Button type="submit" disabled={!canSubmit}>
          {pending && <Loader2 className="animate-spin" />}
          {pending ? t("members.invite.submitting") : t("members.invite.submit")}
        </Button>
      </DialogFooter>
    </form>
  )
}

/**
 * Multi-select picker. Loops per-user `addOne` so a partial failure
 * doesn't lose other selections; successful adds are dropped from the
 * chip row so a retry only re-runs the failing ones.
 */
function AddExistingForm({
  wsId,
  onPendingChange,
  onClose,
  addOne,
}: {
  wsId: string
  onPendingChange: (pending: boolean) => void
  onClose: () => void
  addOne: (body: AddWorkspaceMemberRequest) => Promise<unknown>
}) {
  const { t } = useTranslation("admin")
  const [selected, setSelected] = useState<PlatformUser[]>([])
  const [role, setRole] = useState<MemberRole>("member")
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null)
  const [failures, setFailures] = useState<Array<{ user: PlatformUser; message: string }>>([])
  const [lastSuccess, setLastSuccess] = useState(0)

  const pending = progress !== null
  const canSubmit = selected.length > 0 && !pending

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!canSubmit) return
    setFailures([])
    setProgress({ done: 0, total: selected.length })
    onPendingChange(true)
    const fails: Array<{ user: PlatformUser; message: string }> = []
    for (let i = 0; i < selected.length; i++) {
      const u = selected[i]
      try {
        await addOne({ email: u.email, name: u.name || undefined, role })
      } catch (err) {
        const msg = err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err)
        fails.push({ user: u, message: msg })
      }
      setProgress({ done: i + 1, total: selected.length })
    }
    onPendingChange(false)
    if (fails.length === 0) {
      onClose()
      return
    }
    // Drop successfully-added users from the chip row so a retry only
    // re-runs the failing ones.
    const failedIds = new Set(fails.map((f) => f.user.id))
    setLastSuccess(selected.length - fails.length)
    setSelected((prev) => prev.filter((u) => failedIds.has(u.id)))
    setFailures(fails)
    setProgress(null)
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div className="flex flex-col gap-3">
        <Field label={t("members.add.field.user")}>
          <UserSearchCombobox excludeWorkspace={wsId} selected={selected} onChange={setSelected} disabled={pending} />
        </Field>
        <Field label={t("members.add.field.role")} htmlFor="add-role">
          <Select id="add-role" value={role} onChange={(e) => setRole(e.target.value as MemberRole)} disabled={pending}>
            {ROLES.map((r) => (
              <option key={r} value={r}>{t(`members.role.${r}`)}</option>
            ))}
          </Select>
        </Field>
        {failures.length > 0 && (
          <div className="flex items-start gap-1.5 text-sm text-fg">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            <div className="min-w-0">
              <p className="font-medium">
                {t("members.add.partialError.title", {
                  success: lastSuccess,
                  failed: failures.length,
                  defaultValue: "{{success}} succeeded, {{failed}} failed",
                })}
              </p>
              <ul className="m-0 list-none p-0">
                {failures.map((f) => (
                  <li key={f.user.id} className="break-words font-mono text-xs">
                    {f.user.email}: {f.message}
                  </li>
                ))}
              </ul>
            </div>
          </div>
        )}
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose} disabled={pending}>
          {t("members.add.cancel")}
        </Button>
        <Button type="submit" disabled={!canSubmit}>
          {pending && <Loader2 className="animate-spin" />}
          {pending
            ? t("members.add.submitting", {
                done: progress?.done ?? 0,
                total: progress?.total ?? 0,
                defaultValue: "Adding ({{done}}/{{total}})…",
              })
            : selected.length > 1
              ? t("members.add.submitMany", { count: selected.length, defaultValue: "Add {{count}}" })
              : t("members.add.submit")}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** Step two of the link flow: the link, copyable, and one way out. */
function InviteResultView({ result, onClose }: { result: InviteResult; onClose: () => void }) {
  const { t } = useTranslation("admin")
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(result.inviteLink)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      // Link stays visible for manual copy.
    }
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle>{t("members.invite.resultTitle")}</DialogTitle>
        <DialogDescription>{t("members.invite.resultBody", { email: result.email })}</DialogDescription>
      </DialogHeader>
      <Field label={t("members.invite.credential.url")} htmlFor="invite-link">
        <Input id="invite-link" readOnly value={result.inviteLink} className="select-all font-mono text-xs" onFocus={(e) => e.currentTarget.select()} />
      </Field>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={handleCopy}>
          {copied ? <Check strokeWidth={1.5} aria-hidden="true" /> : <Copy strokeWidth={1.5} aria-hidden="true" />}
          {copied ? t("members.invite.copied") : t("members.invite.copyAll")}
        </Button>
        <Button type="button" onClick={onClose}>{t("members.invite.close")}</Button>
      </DialogFooter>
    </>
  )
}

function InlineError({ message }: { message: string }) {
  return (
    <p className="flex items-start gap-1.5 break-words text-sm text-fg">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
      <span>{message}</span>
    </p>
  )
}

/* ------------------------------------------------------------------ */
/*  Confirm remove dialog                                              */
/* ------------------------------------------------------------------ */

function ConfirmRemoveDialog({
  targetLabel,
  pending,
  error,
  onCancel,
  onConfirm,
}: {
  targetLabel: string
  pending: boolean
  error?: ApiError
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <AlertDialog open onOpenChange={(next) => { if (!next && !pending) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("members.remove.title", { name: targetLabel })}</AlertDialogTitle>
          <AlertDialogDescription>{t("members.remove.description")}</AlertDialogDescription>
          {error && <InlineError message={error.message} />}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button variant="outline" disabled={pending}>{t("members.remove.cancel")}</Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button
              variant="destructive"
              disabled={pending}
              onClick={(event) => {
                event.preventDefault()
                onConfirm()
              }}
            >
              {pending && <Loader2 className="animate-spin" />}
              {t("members.remove.confirm")}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
