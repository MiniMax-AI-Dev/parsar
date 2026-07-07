import { useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Check,
  Copy,
  Inbox,
  KeyRound,
  Loader2,
  MoreHorizontal,
  Plus,
  ShieldAlert,
  UserCircle2,
  UserMinus,
  UserPlus,
  Users,
  WifiOff,
  X,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { UserSearchCombobox } from "../../components/UserSearchCombobox"
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
import { Skeleton } from "../../components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../components/ui/table"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import {
  useAddWorkspaceMember,
  useRemoveWorkspaceMember,
  useUpdateWorkspaceMemberRole,
  useWorkspaceMembers,
} from "../../lib/api-members"
import { useBootstrapStatus } from "../../lib/api-bootstrap"
import type {
  AddWorkspaceMemberRequest,
  MemberRole,
  PendingJoinRequest,
  PlatformUser,
  UserStatus,
  WorkspaceMember,
} from "../../lib/api-types"
import {
  useApproveJoinRequest,
  usePendingJoinRequests,
  useRejectJoinRequest,
} from "../../lib/api-workspaces"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"

type Tab = "workspace" | "pending"
const ROLES: MemberRole[] = ["owner", "admin", "member", "viewer"]

/* ------------------------------------------------------------------ */
/*  Helpers                                                            */
/* ------------------------------------------------------------------ */

function RoleBadge({ role }: { role: MemberRole }) {
  const { t } = useTranslation("admin")
  const variant =
    role === "owner"
      ? "primary"
      : role === "admin"
        ? "warning"
        : role === "member"
          ? "neutral"
          : "neutral"
  return (
    <Badge variant={variant} dot>
      {t(`members.role.${role}`)}
    </Badge>
  )
}

function UserStatusBadge({ status }: { status: UserStatus }) {
  const { t } = useTranslation("admin")
  if (status === "active") {
    return <Badge variant="success">{t("members.userStatus.active")}</Badge>
  }
  return <Badge variant="neutral">{t("members.userStatus.disabled")}</Badge>
}

function UserCell({ name, email }: { name: string; email: string }) {
  return (
    <div className="flex items-center gap-2.5">
      <div className="rounded-full bg-surface-muted p-1.5">
        <UserCircle2 className="h-4 w-4 text-fg-subtle" strokeWidth={1.75} />
      </div>
      <div className="min-w-0">
        <div className="truncate text-sm font-medium text-fg">
          {name || "—"}
        </div>
        <div className="truncate text-xs text-fg-subtle">{email}</div>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export function MembersPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const [tab, setTab] = useState<Tab>("workspace")
  const [addWsOpen, setAddWsOpen] = useState(false)
  const [inviteOpen, setInviteOpen] = useState(false)
  const [removeWsTarget, setRemoveWsTarget] = useState<WorkspaceMember | null>(null)

  const wsQ = useWorkspaceMembers(wsId)
  const addWsMut = useAddWorkspaceMember(wsId)
  const updateWsRoleMut = useUpdateWorkspaceMemberRole(wsId)
  const removeWsMut = useRemoveWorkspaceMember(wsId)

  const isMockWs = !wsId

  const handleWsRoleChange = async (m: WorkspaceMember, role: MemberRole) => {
    if (role === m.role) return
    try {
      await updateWsRoleMut.mutateAsync({ userId: m.user_id, role })
    } catch {
      void wsQ.refetch()
    }
  }

  return (
    <AdminLayout activeMenu="members">
      <PageHeader
        title={t("members.page.title")}
        action={
          tab === "workspace" ? (
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => setInviteOpen(true)}
                disabled={!wsId}
                title={!wsId ? t("members.add.requiresWorkspace") : undefined}
              >
                <UserPlus className="h-3.5 w-3.5" />
                {t("members.invite.cta")}
              </Button>
              <Button
                size="sm"
                onClick={() => setAddWsOpen(true)}
                disabled={!wsId}
                title={!wsId ? t("members.add.requiresWorkspace") : undefined}
              >
                <Plus className="h-3.5 w-3.5" />
                {t("members.add.cta")}
              </Button>
            </div>
          ) : null
        }
      />

      <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
        <TabsList>
          <TabsTrigger value="workspace">
            <Users className="h-3.5 w-3.5" />
            {t("members.tabs.workspace")}
            {wsQ.data && (
              <span className="ml-1 text-xs text-fg-subtle">
                ({wsQ.data.members.length})
              </span>
            )}
          </TabsTrigger>
          {/* Pending approval — visible even when count is 0 so admins know
              the surface exists. Inner empty state guides workspace
              picking rather than disabling the tab. */}
          <TabsTrigger value="pending">
            <Inbox className="h-3.5 w-3.5" />
            {t("members.tabs.pending")}
            <PendingBadge wsId={wsId} />
          </TabsTrigger>
        </TabsList>

        {/* === Workspace tab ========================================= */}
        <TabsContent value="workspace" className="space-y-3">
          {isMockWs && (
            <MockBanner hint={t("members.mockBanner.ws")} />
          )}
          {updateWsRoleMut.error && (
            <ErrorBanner message={(updateWsRoleMut.error as ApiError).message} />
          )}
          {removeWsMut.error && (
            <ErrorBanner message={(removeWsMut.error as ApiError).message} />
          )}
          <MembersTable
            loading={wsQ.isLoading}
            error={wsQ.isError && !isMockWs ? (wsQ.error as ApiError) : undefined}
            onRetry={() => void wsQ.refetch()}
            members={wsQ.data?.members ?? []}
            emptyTitle={t("members.empty.ws.title")}
            emptyDescription={t("members.empty.ws.description")}
            writable={!isMockWs}
            onChangeRole={(m, role) => handleWsRoleChange(m, role)}
            onRemove={(m) => setRemoveWsTarget(m)}
            roleChangePending={updateWsRoleMut.isPending}
          />
        </TabsContent>

        {/* === Pending join requests tab ============================= */}
        <TabsContent value="pending" className="space-y-3">
          <PendingJoinRequestsTab wsId={wsId} />
        </TabsContent>
      </Tabs>

      {/* === Workspace dialogs ===================================== */}
      {addWsOpen && wsId && (
        <AddMemberDialog
          excludeWorkspace={wsId}
          onClose={() => {
            setAddWsOpen(false)
            addWsMut.reset()
          }}
          addOne={(body) => addWsMut.mutateAsync(body)}
        />
      )}

      {inviteOpen && wsId && (
        <InviteMemberDialog
          onClose={() => {
            setInviteOpen(false)
            addWsMut.reset()
          }}
          invite={(body) => addWsMut.mutateAsync(body)}
        />
      )}

      {removeWsTarget && (
        <ConfirmRemoveDialog
          targetLabel={removeWsTarget.user_name || removeWsTarget.user_email}
          description={t("members.remove.description")}
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
/*  Members table (shared by both tabs)                                */
/* ------------------------------------------------------------------ */

interface MembersTableProps {
  loading: boolean
  error?: ApiError
  onRetry: () => void
  members: WorkspaceMember[]
  emptyTitle: string
  emptyDescription: string
  /** When true, render the actions column (role dropdown + remove button). */
  writable?: boolean
  onChangeRole?: (m: WorkspaceMember, role: MemberRole) => void
  onRemove?: (m: WorkspaceMember) => void
  roleChangePending?: boolean
}

function MembersTable({
  loading,
  error,
  onRetry,
  members,
  emptyTitle,
  emptyDescription,
  writable = false,
  onChangeRole,
  onRemove,
  roleChangePending = false,
}: MembersTableProps) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()

  if (loading) {
    return (
      <div className="space-y-2 rounded-lg border border-line bg-surface p-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    )
  }

  if (error) {
    return (
      <ErrorState
        title={
          error.envelope?.unreachable
            ? t("members.error.unreachable.title")
            : t("members.error.load.title")
        }
        description={
          error.envelope?.unreachable
            ? t("members.error.unreachable.description")
            : error.message ?? t("members.error.load.description")
        }
        hint={
          error.envelope?.unreachable
            ? t("members.error.unreachable.hint")
            : t("members.error.load.hint")
        }
        onRetry={onRetry}
      />
    )
  }

  if (members.length === 0) {
    return (
      <EmptyState
        icon={Users}
        title={emptyTitle}
        description={emptyDescription}
      />
    )
  }

  return (
    <div className="overflow-hidden rounded-lg border border-line bg-surface">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("members.table.user")}</TableHead>
            <TableHead>{t("members.table.role")}</TableHead>
            <TableHead>{t("members.table.userStatus")}</TableHead>
            <TableHead>{t("members.table.joinedAt")}</TableHead>
            {writable && (
              <TableHead className="text-right">
                {t("members.table.actions")}
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {members.map((m) => (
            <TableRow key={m.id}>
              <TableCell>
                <UserCell name={m.user_name} email={m.user_email} />
              </TableCell>
              <TableCell>
                {writable && onChangeRole ? (
                  <select
                    value={m.role}
                    disabled={roleChangePending}
                    onChange={(e) =>
                      onChangeRole(m, e.target.value as MemberRole)
                    }
                    className="rounded-md border border-line bg-surface px-2 py-1 text-sm text-fg focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
                  >
                    {ROLES.map((r) => (
                      <option key={r} value={r}>
                        {t(`members.role.${r}`)}
                      </option>
                    ))}
                  </select>
                ) : (
                  <RoleBadge role={m.role} />
                )}
              </TableCell>
              <TableCell>
                <UserStatusBadge status={m.user_status} />
              </TableCell>
              <TableCell>
                <span className="text-sm text-fg-subtle">
                  {fmtAgo(m.created_at)}
                </span>
              </TableCell>
              {writable && (
                <TableCell className="text-right">
                  {onRemove ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onRemove(m)}
                      title={t("members.remove.cta")}
                    >
                      <UserMinus className="h-3.5 w-3.5 text-fg-subtle" />
                    </Button>
                  ) : (
                    <MoreHorizontal className="ml-auto h-4 w-4 text-fg-faint" />
                  )}
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Add workspace member dialog                                        */
/* ------------------------------------------------------------------ */

/**
 * Multi-select picker. Loops per-user `addOne` so a partial failure
 * doesn't lose other selections; successful adds are dropped from the
 * chip row so a retry only re-runs the failing ones.
 */
function AddMemberDialog({
  excludeWorkspace,
  onClose,
  addOne,
}: {
  excludeWorkspace: string
  onClose: () => void
  addOne: (body: AddWorkspaceMemberRequest) => Promise<unknown>
}) {
  const { t } = useTranslation("admin")
  const [selected, setSelected] = useState<PlatformUser[]>([])
  const [role, setRole] = useState<MemberRole>("member")
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(
    null
  )
  const [failures, setFailures] = useState<
    Array<{ user: PlatformUser; message: string }>
  >([])

  const pending = progress !== null
  const canSubmit = selected.length > 0 && !pending

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!canSubmit) return
    setFailures([])
    setProgress({ done: 0, total: selected.length })
    const fails: Array<{ user: PlatformUser; message: string }> = []
    for (let i = 0; i < selected.length; i++) {
      const u = selected[i]
      try {
        await addOne({ email: u.email, name: u.name || undefined, role })
      } catch (err) {
        const msg =
          err instanceof ApiError
            ? err.message
            : err instanceof Error
              ? err.message
              : String(err)
        fails.push({ user: u, message: msg })
      }
      setProgress({ done: i + 1, total: selected.length })
    }
    if (fails.length === 0) {
      onClose()
      return
    }
    // Drop successfully-added users from the chip row so a retry only
    // re-runs the failing ones.
    const failedIds = new Set(fails.map((f) => f.user.id))
    setSelected((prev) => prev.filter((u) => failedIds.has(u.id)))
    setFailures(fails)
    setProgress(null)
  }

  const successCount = progress
    ? progress.done - failures.length
    : selected.length === 0
      ? 0
      : 0

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next && !pending) onClose()
      }}
    >
      <DialogContent className="max-w-md gap-0 p-0">
        <form onSubmit={handleSubmit}>
          <DialogHeader className="border-b border-line-muted px-5 py-3 pr-10">
            <DialogTitle className="text-sm">
              {t("members.add.title")}
            </DialogTitle>
          </DialogHeader>

          <div className="space-y-3 px-5 py-4">
            <DialogField label={t("members.add.field.user")} required>
              <UserSearchCombobox
                excludeWorkspace={excludeWorkspace}
                selected={selected}
                onChange={setSelected}
                disabled={pending}
              />
            </DialogField>

            <DialogField label={t("members.add.field.role")} required>
              <select
                value={role}
                onChange={(e) => setRole(e.target.value as MemberRole)}
                disabled={pending}
                className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-fg focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {t(`members.role.${r}`)}
                  </option>
                ))}
              </select>
            </DialogField>

            {failures.length > 0 && (
              <div className="space-y-1 rounded-md border border-danger-border bg-danger-subtle px-3 py-2">
                <p className="text-sm font-medium text-danger-emphasis">
                  {t("members.add.partialError.title", {
                    success: successCount,
                    failed: failures.length,
                    defaultValue:
                      "{{success}} succeeded, {{failed}} failed",
                  })}
                </p>
                <ul className="space-y-0.5">
                  {failures.map((f) => (
                    <li
                      key={f.user.id}
                      className="text-xs text-danger-emphasis"
                    >
                      {f.user.email}: {f.message}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>

          <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onClose}
              disabled={pending}
            >
              {t("members.add.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={!canSubmit}>
              {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {pending
                ? t("members.add.submitting", {
                    done: progress?.done ?? 0,
                    total: progress?.total ?? 0,
                    defaultValue: "Adding ({{done}}/{{total}})…",
                  })
                : selected.length > 1
                  ? t("members.add.submitMany", {
                      count: selected.length,
                      defaultValue: "Add {{count}}",
                    })
                  : t("members.add.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/* ------------------------------------------------------------------ */
/*  Invite member dialog                                               */
/*                                                                     */
/*  Two-step: (1) form → POST /members {invite:true} → (2) result      */
/*  screen shows the plaintext temp password exactly once. The         */
/*  password is held in local state; if the admin closes the dialog    */
/*  without copying, it is gone forever (there is no read-back API).   */
/* ------------------------------------------------------------------ */

interface InviteResult {
  email: string
  tempPassword: string
  userCreated: boolean
}

function InviteMemberDialog({
  onClose,
  invite,
}: {
  onClose: () => void
  invite: (body: AddWorkspaceMemberRequest) => Promise<AddWorkspaceMemberResponseLike>
}) {
  const { t } = useTranslation("admin")
  const bootstrapQ = useBootstrapStatus()
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [role, setRole] = useState<MemberRole>("member")
  const [pending, setPending] = useState(false)
  const [errMsg, setErrMsg] = useState<string | null>(null)
  const [result, setResult] = useState<InviteResult | null>(null)
  const [copied, setCopied] = useState(false)

  const canSubmit = email.trim() !== "" && !pending

  // Login URL priority: operator-configured PARSAR_PUBLIC_URL (comes
  // through bootstrap.status.public_url — the same trusted source the
  // pairing dialog uses), else current window origin. Trailing slash
  // stripped so we can always append the path with a single "/".
  const loginURL = (() => {
    const raw = (bootstrapQ.data?.public_url ?? "").trim()
    const base = raw !== "" ? raw : window.location.origin
    return base.replace(/\/+$/, "") + "/login"
  })()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!canSubmit) return
    setErrMsg(null)
    setPending(true)
    try {
      const res = await invite({
        email: email.trim(),
        name: name.trim() || undefined,
        role,
        invite: true,
      })
      setResult({
        email: res.member.user_email,
        tempPassword: res.temp_password ?? "",
        userCreated: res.user_created,
      })
    } catch (err) {
      setErrMsg(
        err instanceof ApiError
          ? err.message
          : err instanceof Error
            ? err.message
            : String(err),
      )
    } finally {
      setPending(false)
    }
  }

  // The copy payload bundles all three pieces the invitee needs so
  // the admin can paste it into any IM verbatim. Format is stable and
  // machine-parseable enough for the invitee to eyeball.
  const copyPayload = result
    ? [
        `Sign in: ${loginURL}`,
        `Email: ${result.email}`,
        `Password: ${result.tempPassword}`,
      ].join("\n")
    : ""

  const handleCopy = async () => {
    if (!copyPayload) return
    try {
      await navigator.clipboard.writeText(copyPayload)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard write can fail on non-HTTPS or older browsers; the
      // credentials are still visible in the modal so the admin can
      // copy them by hand. Silently swallow.
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next && !pending) onClose()
      }}
    >
      <DialogContent className="max-w-md gap-0 p-0">
        {result === null ? (
          <form onSubmit={handleSubmit}>
            <DialogHeader className="space-y-1 border-b border-line-muted px-5 py-3 pr-10">
              <DialogTitle className="text-sm">
                {t("members.invite.title")}
              </DialogTitle>
              <DialogDescription className="text-xs leading-relaxed">
                {t("members.invite.description")}
              </DialogDescription>
            </DialogHeader>

            <div className="space-y-3 px-5 py-4">
              <DialogField label={t("members.invite.emailLabel")} required>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder={t("members.invite.emailPlaceholder")}
                  autoComplete="off"
                  required
                  disabled={pending}
                  className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-fg placeholder:text-fg-faint focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
                />
              </DialogField>

              <DialogField label={t("members.invite.nameLabel")}>
                <input
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={t("members.invite.namePlaceholder")}
                  autoComplete="off"
                  disabled={pending}
                  className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-fg placeholder:text-fg-faint focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
                />
              </DialogField>

              <DialogField label={t("members.invite.roleLabel")} required>
                <select
                  value={role}
                  onChange={(e) => setRole(e.target.value as MemberRole)}
                  disabled={pending}
                  className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-fg focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
                >
                  {ROLES.map((r) => (
                    <option key={r} value={r}>
                      {t(`members.role.${r}`)}
                    </option>
                  ))}
                </select>
              </DialogField>

              {errMsg && (
                <p className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-xs text-danger-emphasis">
                  {errMsg}
                </p>
              )}
            </div>

            <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={onClose}
                disabled={pending}
              >
                {t("members.invite.cancel")}
              </Button>
              <Button type="submit" size="sm" disabled={!canSubmit}>
                {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {pending ? t("members.invite.submitting") : t("members.invite.submit")}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div>
            <DialogHeader className="space-y-1 border-b border-line-muted px-5 py-3 pr-10">
              <DialogTitle className="flex items-center gap-2 text-sm">
                <KeyRound className="h-4 w-4 text-success" />
                {t("members.invite.resultTitle")}
              </DialogTitle>
              <DialogDescription className="text-xs leading-relaxed">
                {t("members.invite.resultBody", { email: result.email })}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-3 px-5 py-4">
              {result.userCreated && result.tempPassword ? (
                <>
                  <div className="space-y-2 rounded-md border border-line bg-surface-subtle/60 p-3">
                    <InviteCredentialRow
                      label={t("members.invite.credential.url")}
                      value={loginURL}
                    />
                    <InviteCredentialRow
                      label={t("members.invite.credential.email")}
                      value={result.email}
                    />
                    <InviteCredentialRow
                      label={t("members.invite.credential.password")}
                      value={result.tempPassword}
                      mono
                    />
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={handleCopy}
                    className="w-full"
                  >
                    <Copy className="h-3.5 w-3.5" />
                    {copied ? t("members.invite.copied") : t("members.invite.copyAll")}
                  </Button>
                </>
              ) : (
                <p className="rounded-md border border-warning-border bg-warning-subtle/40 px-3 py-2 text-xs text-warning-emphasis">
                  {t("members.invite.existingUserNotice")}
                </p>
              )}
            </div>
            <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
              <Button type="button" size="sm" onClick={onClose}>
                {t("members.invite.close")}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

function InviteCredentialRow({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="flex items-baseline gap-3">
      <span className="w-20 shrink-0 text-xs font-medium uppercase tracking-wide text-fg-faint">
        {label}
      </span>
      <span
        className={`min-w-0 flex-1 select-all break-all text-sm text-fg ${mono ? "font-mono" : ""}`}
      >
        {value}
      </span>
    </div>
  )
}

/**
 * Local structural type used to loosen invite()'s parameter — the
 * useAddWorkspaceMember mutation returns AddWorkspaceMemberResponse
 * but its `temp_password` is only populated on the invite path. We
 * type explicitly so the invite dialog does not accidentally read
 * fields the store hook does not surface.
 */
interface AddWorkspaceMemberResponseLike {
  member: { user_email: string }
  user_created: boolean
  temp_password?: string
}

function DialogField({
  label,
  required,
  children,
}: {
  label: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <label className="block space-y-1">
      <span className="text-sm font-medium text-fg-muted">
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </span>
      {children}
    </label>
  )
}

/* ------------------------------------------------------------------ */
/*  Confirm remove dialog                                              */
/* ------------------------------------------------------------------ */

function ConfirmRemoveDialog({
  targetLabel,
  description,
  confirmLabel,
  pending,
  error,
  onCancel,
  onConfirm,
}: {
  targetLabel: string
  description: string
  confirmLabel?: string
  pending: boolean
  error?: ApiError
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <Dialog open onOpenChange={(next) => { if (!next && !pending) onCancel() }}>
      <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
        <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5">
          <div className="shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis">
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">
              {t("members.remove.title", { name: targetLabel })}
            </DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">
              {description}
            </DialogDescription>
            {error && (
              <p className="text-sm text-danger-emphasis">{error.message}</p>
            )}
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
          <Button
            variant="outline"
            size="sm"
            onClick={onCancel}
            disabled={pending}
          >
            {t("members.remove.cancel")}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={onConfirm}
            disabled={pending}
          >
            {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {confirmLabel ?? t("members.remove.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/* ------------------------------------------------------------------ */
/*  Error banner (mutation-level errors above the table)               */
/* ------------------------------------------------------------------ */

function ErrorBanner({ message }: { message: string }) {
  const { t } = useTranslation("admin")
  return (
    <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2">
      <p className="text-sm font-medium text-danger-emphasis">
        {t("members.mutation.errorTitle")}
      </p>
      <p className="text-xs text-danger-emphasis">{message}</p>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Mock banner                                                        */
/* ------------------------------------------------------------------ */

function MockBanner({
  hint,
}: {
  hint: string
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="flex items-start gap-2.5 rounded-lg border border-warning-border bg-warning-subtle/40 p-3">
      <WifiOff
        className="mt-0.5 h-4 w-4 shrink-0 text-warning"
        strokeWidth={2}
      />
      <div className="space-y-0.5">
        <p className="text-sm font-medium text-warning-emphasis">
          {t("members.mockBanner.title", {
            scope: t("members.tabs.workspace"),
          })}
        </p>
        <p className="text-sm leading-relaxed text-warning-emphasis">{hint}</p>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Pending join requests tab                                          */
/*                                                                     */
/*  Approval UI for user-initiated "join workspace" requests. The server       */
/*  does not treat workspace_join_requests as a separate table — they are      */
/*  just rows in workspace_members with status='pending' (see server SQL       */
/*  notes). The frontend splits them into their own tab, distinct from the     */
/*  active-members list, to keep the admin surface simple.                     */
/* ------------------------------------------------------------------ */

function PendingBadge({ wsId }: { wsId: string | null }) {
  // Fetch via query, use length as the badge count. The tab is always mounted
  // on the parent so this query stays alive; a 60s staleTime keeps the
  // network cost in check.
  const q = usePendingJoinRequests(wsId)
  const count = q.data?.requests.length ?? 0
  if (count === 0) return null
  return (
    <span className="ml-1 rounded-full bg-warning-subtle px-1.5 py-0 text-xs font-medium text-warning">
      {count}
    </span>
  )
}

function PendingJoinRequestsTab({ wsId }: { wsId: string | null }) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const q = usePendingJoinRequests(wsId)
  const approveMut = useApproveJoinRequest()
  const rejectMut = useRejectJoinRequest()
  const [confirmReject, setConfirmReject] =
    useState<PendingJoinRequest | null>(null)

  if (!wsId) {
    return (
      <EmptyState
        icon={Inbox}
        title={t("members.add.requiresWorkspace")}
      />
    )
  }
  if (q.isLoading) {
    return <Skeleton className="h-24 w-full" />
  }
  const requests = q.data?.requests ?? []
  if (requests.length === 0) {
    return (
      <EmptyState
        icon={Inbox}
        title={t("members.pendingRequests.empty")}
      />
    )
  }

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("members.table.user")}</TableHead>
            <TableHead>{t("members.pendingRequests.reason")}</TableHead>
            <TableHead>{t("members.pendingRequests.requestedAt")}</TableHead>
            <TableHead className="w-[160px]" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {requests.map((req) => (
            <PendingRequestRow
              key={req.id}
              wsId={wsId}
              req={req}
              approving={approveMut.isPending}
              rejecting={rejectMut.isPending}
              onApprove={() =>
                approveMut.mutate({ wsId, requestId: req.id, request: req })
              }
              onReject={() => setConfirmReject(req)}
            />
          ))}
        </TableBody>
      </Table>

      {/*
        Reject confirmation. Not using ConfirmArchiveDialog (that uses the
        destructive red styling; rejecting isn't destructive — the applicant
        can re-apply later). A lightweight Dialog with a simple confirm flow
        is enough.
      */}
      <Dialog
        open={confirmReject !== null}
        onOpenChange={(open) => {
          if (!open) {
            setConfirmReject(null)
            rejectMut.reset()
          }
        }}
      >
        <DialogContent showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>
              {t("members.pendingRequests.confirmRejectTitle")}
            </DialogTitle>
            <DialogDescription>
              {t("members.pendingRequests.confirmRejectBody", {
                name: confirmReject?.user_name || confirmReject?.user_email || "",
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setConfirmReject(null)}
              disabled={rejectMut.isPending}
            >
              {tc("actions.cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={() => {
                if (!confirmReject) return
                rejectMut.mutate(
                  {
                    wsId,
                    requestId: confirmReject.id,
                    request: confirmReject,
                  },
                  {
                    onSuccess: () => setConfirmReject(null),
                  }
                )
              }}
              disabled={rejectMut.isPending}
            >
              {rejectMut.isPending
                ? tc("states.loading")
                : t("members.pendingRequests.actions.reject")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function PendingRequestRow({
  req,
  approving,
  rejecting,
  onApprove,
  onReject,
}: {
  wsId: string
  req: PendingJoinRequest
  approving: boolean
  rejecting: boolean
  onApprove: () => void
  onReject: () => void
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  return (
    <TableRow>
      <TableCell>
        <div className="flex flex-col">
          <span className="text-sm font-medium text-fg">
            {req.user_name || req.user_email}
          </span>
          <span className="text-xs text-fg-subtle">{req.user_email}</span>
        </div>
      </TableCell>
      <TableCell className="text-sm text-fg-muted">
        {req.request_reason || (
          <span className="text-fg-faint">
            {t("members.pendingRequests.noReason")}
          </span>
        )}
      </TableCell>
      <TableCell className="text-sm text-fg-subtle">
        {fmtAgo(req.requested_at)}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex justify-end gap-1.5">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={approving || rejecting}
            onClick={onReject}
          >
            <X className="h-3.5 w-3.5" />
            {t("members.pendingRequests.actions.reject")}
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={approving || rejecting}
            onClick={onApprove}
          >
            <Check className="h-3.5 w-3.5" />
            {t("members.pendingRequests.actions.approve")}
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}
