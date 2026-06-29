import { useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Check,
  Inbox,
  Loader2,
  MoreHorizontal,
  Plus,
  ShieldAlert,
  UserCircle2,
  UserMinus,
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
      <div className="rounded-full bg-slate-100 p-1.5">
        <UserCircle2 className="h-4 w-4 text-slate-500" strokeWidth={1.75} />
      </div>
      <div className="min-w-0">
        <div className="truncate text-[13px] font-medium text-slate-900">
          {name || "—"}
        </div>
        <div className="truncate text-[12px] text-slate-500">{email}</div>
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
            <Button
              size="sm"
              onClick={() => setAddWsOpen(true)}
              disabled={!wsId}
              title={!wsId ? t("members.add.requiresWorkspace") : undefined}
            >
              <Plus className="h-3.5 w-3.5" />
              {t("members.add.cta")}
            </Button>
          ) : null
        }
      />

      <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
        <TabsList>
          <TabsTrigger value="workspace">
            <Users className="h-3.5 w-3.5" />
            {t("members.tabs.workspace")}
            {wsQ.data && (
              <span className="ml-1 text-[12px] text-slate-500">
                ({wsQ.data.members.length})
              </span>
            )}
          </TabsTrigger>
          {/* 待审批申请 — visible even when count is 0 so admins know
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
      <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-4">
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
    <div className="overflow-hidden rounded-lg border border-slate-200 bg-white">
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
                    className="rounded-md border border-slate-200 bg-white px-2 py-1 text-[13px] text-slate-900 focus:border-slate-400 focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
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
                <span className="text-[13px] text-slate-500">
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
                      <UserMinus className="h-3.5 w-3.5 text-slate-500" />
                    </Button>
                  ) : (
                    <MoreHorizontal className="ml-auto h-4 w-4 text-slate-300" />
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
          <DialogHeader className="border-b border-slate-100 px-5 py-3 pr-10">
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
                className="w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-[13px] text-slate-900 focus:border-slate-400 focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {t(`members.role.${r}`)}
                  </option>
                ))}
              </select>
            </DialogField>

            {failures.length > 0 && (
              <div className="space-y-1 rounded-md border border-red-200 bg-red-50 px-3 py-2">
                <p className="text-[13px] font-medium text-red-900">
                  {t("members.add.partialError.title", {
                    success: successCount,
                    failed: failures.length,
                    defaultValue:
                      "成功 {{success}} 个,失败 {{failed}} 个",
                  })}
                </p>
                <ul className="space-y-0.5">
                  {failures.map((f) => (
                    <li
                      key={f.user.id}
                      className="text-[12px] text-red-700"
                    >
                      {f.user.email}: {f.message}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>

          <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
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
                    defaultValue: "添加中 ({{done}}/{{total}})…",
                  })
                : selected.length > 1
                  ? t("members.add.submitMany", {
                      count: selected.length,
                      defaultValue: "添加 {{count}} 个",
                    })
                  : t("members.add.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
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
      <span className="text-[13px] font-medium text-slate-700">
        {label}
        {required && <span className="ml-0.5 text-red-500">*</span>}
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
          <div className="shrink-0 rounded-full bg-red-100 p-2 text-red-700">
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
              <p className="text-[13px] text-red-700">{error.message}</p>
            )}
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
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
    <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2">
      <p className="text-[13px] font-medium text-red-900">
        {t("members.mutation.errorTitle")}
      </p>
      <p className="text-[12px] text-red-700">{message}</p>
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
    <div className="flex items-start gap-2.5 rounded-lg border border-amber-200 bg-amber-50/40 p-3">
      <WifiOff
        className="mt-0.5 h-4 w-4 shrink-0 text-amber-600"
        strokeWidth={2}
      />
      <div className="space-y-0.5">
        <p className="text-[13px] font-medium text-amber-900">
          {t("members.mockBanner.title", {
            scope: t("members.tabs.workspace"),
          })}
        </p>
        <p className="text-[13px] leading-relaxed text-amber-800">{hint}</p>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Pending join requests tab                                          */
/*                                                                     */
/*  这里专门处理"用户主动申请加入工作区"的审批 UI。后端不在乎              */
/*  workspace_join_requests 是不是独立表 —— 它就是 workspace_members      */
/*  里 status='pending' 的行(详见 server SQL 注释)。前端把这一组         */
/*  抽到独立 Tab,跟"已成成员"的列表分开,降低管理员的认知负担。           */
/* ------------------------------------------------------------------ */

function PendingBadge({ wsId }: { wsId: string | null }) {
  // 我们用 query 拿数,然后用 length 当作 badge。Tab 是父级常驻的,
  // 所以 query 一直会跑;但 staleTime 60s 让网络成本可控。
  const q = usePendingJoinRequests(wsId)
  const count = q.data?.requests.length ?? 0
  if (count === 0) return null
  return (
    <span className="ml-1 rounded-full bg-amber-100 px-1.5 py-0 text-[11px] font-medium text-amber-700">
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
        拒绝二次确认。我们没有用 ConfirmArchiveDialog(那是 destructive
        红色样式,语义上 reject 不是 destructive — 申请人之后还能再申请),
        直接用一个轻量 Dialog 走简单 confirm 流程。
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
          <span className="text-[13px] font-medium text-slate-900">
            {req.user_name || req.user_email}
          </span>
          <span className="text-[12px] text-slate-500">{req.user_email}</span>
        </div>
      </TableCell>
      <TableCell className="text-[13px] text-slate-700">
        {req.request_reason || (
          <span className="text-slate-400">
            {t("members.pendingRequests.noReason")}
          </span>
        )}
      </TableCell>
      <TableCell className="text-[13px] text-slate-500">
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
