// Memory admin page — user / project tabbed list.
// Tab state is local on purpose: switching tabs shouldn't pollute
// history, so back-button skips ephemeral tab toggles.

import { useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Activity,
  BrainCircuit,
  Loader2,
  Plus,
  ShieldCheck,
  Trash2,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ResourceAuditTimeline } from "../../components/admin/ResourceAuditTimeline"
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
import { Skeleton } from "../../components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import {
  useCreateMemoryMutation,
  useDeleteMemoryMutation,
  useProjectMemoriesQuery,
  useUpdateMemoryMutation,
  useUserMemoriesQuery,
  type Memory,
  type MemoryScope,
  type MemoryType,
} from "../../lib/api-memory"
import { useRelativeTime } from "../../lib/relative-time"
import { useProjectId } from "../../lib/workspace"
import type { SpecSource } from "../../lib/api-specs"

const MEMORY_TYPES: MemoryType[] = ["user", "feedback", "project", "reference"]

type TypeFilter = MemoryType | "all"

interface EditorState {
  mode: "create" | "edit"
  scope: MemoryScope
  projectID?: string
  memory?: Memory
}

export function MemoryPage() {
  const { t } = useTranslation("admin")
  const [tab, setTab] = useState<MemoryScope>("user")

  return (
    <AdminLayout activeMenu="settings">
      <PageHeader title={t("memory.page.title")} />
      <SettingsTabs active="memory" />

      <Tabs value={tab} onValueChange={(value) => setTab(value as MemoryScope)}>
        <TabsList>
          <TabsTrigger value="user">{t("memory.tabs.user")}</TabsTrigger>
          <TabsTrigger value="project">{t("memory.tabs.project")}</TabsTrigger>
        </TabsList>
        <TabsContent value="user">
          <UserMemoryPanel />
        </TabsContent>
        <TabsContent value="project">
          <ProjectMemoryPanel />
        </TabsContent>
      </Tabs>
    </AdminLayout>
  )
}

// ----- user memory panel ----------------------------------------------------

function UserMemoryPanel() {
  const { t } = useTranslation("admin")
  const listQ = useUserMemoriesQuery()
  return (
    <MemoryPanelBody
      memories={listQ.data?.memories}
      isLoading={listQ.isLoading}
      isError={listQ.isError}
      error={listQ.error as ApiError | undefined}
      onRetry={() => void listQ.refetch()}
      emptyLabel={t("memory.empty.user")}
      scope="user"
      projectID={undefined}
    />
  )
}

// ----- project memory panel -------------------------------------------------

function ProjectMemoryPanel() {
  const { t } = useTranslation("admin")
  const projectID = useProjectId()
  const listQ = useProjectMemoriesQuery(projectID)
  if (!projectID) {
    return <ScopeRequiredState scope="project" resourceName={t("memory.page.title")} />
  }
  return (
    <MemoryPanelBody
      memories={listQ.data?.memories}
      isLoading={listQ.isLoading}
      isError={listQ.isError}
      error={listQ.error as ApiError | undefined}
      onRetry={() => void listQ.refetch()}
      emptyLabel={t("memory.empty.project")}
      scope="project"
      projectID={projectID}
    />
  )
}

// ----- shared panel body ----------------------------------------------------

interface MemoryPanelBodyProps {
  memories?: Memory[]
  isLoading: boolean
  isError: boolean
  error?: ApiError
  onRetry: () => void
  emptyLabel: string
  scope: MemoryScope
  projectID?: string
}

function MemoryPanelBody({
  memories,
  isLoading,
  isError,
  error,
  onRetry,
  emptyLabel,
  scope,
  projectID,
}: MemoryPanelBodyProps) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const createMut = useCreateMemoryMutation()
  const updateMut = useUpdateMemoryMutation()
  const deleteMut = useDeleteMemoryMutation()

  const [filter, setFilter] = useState<TypeFilter>("all")
  const [editor, setEditor] = useState<EditorState | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Memory | null>(null)
  // Project-scope only — user-scope rows hide the entry button because
  // /api/v1/projects/.../audit-records can't surface their events.
  const [auditFor, setAuditFor] = useState<Memory | null>(null)

  const closeEditor = () => {
    setEditor(null)
    createMut.reset()
    updateMut.reset()
  }

  const handleEditorSubmit = async (input: EditorSubmitInput) => {
    if (!editor) return
    if (editor.mode === "create") {
      await createMut.mutateAsync({
        scope,
        project_id: projectID,
        memory_type: input.memoryType,
        title: input.title || undefined,
        body: input.body,
        why: input.why || undefined,
        tags: input.tags,
      })
    } else if (editor.memory) {
      await updateMut.mutateAsync({
        memoryID: editor.memory.id,
        scope,
        projectID,
        body: {
          title: input.title,
          body: input.body,
          why: input.why,
          tags: input.tags,
        },
      })
    }
    closeEditor()
  }

  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    try {
      await deleteMut.mutateAsync({
        memoryID: confirmDelete.id,
        scope,
        projectID,
      })
      setConfirmDelete(null)
    } catch {
      /* error renders inline */
    }
  }

  const rows = memories ?? []
  const filtered = filter === "all" ? rows : rows.filter((m) => m.memory_type === filter)

  return (
    <>
      <div className="mt-2 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-1">
          <TypeFilterChip
            label={t("memory.filter.all")}
            active={filter === "all"}
            onClick={() => setFilter("all")}
          />
          {MEMORY_TYPES.map((type) => (
            <TypeFilterChip
              key={type}
              label={t(`memory.type.${type}` as never)}
              active={filter === type}
              onClick={() => setFilter(type)}
            />
          ))}
        </div>
        <Button
          type="button"
          size="sm"
          onClick={() =>
            setEditor({ mode: "create", scope, projectID })
          }
        >
          <Plus className="h-3.5 w-3.5" />
          {t("memory.actions.create")}
        </Button>
      </div>

      <div className="mt-4">
        {isLoading ? (
          <div className="space-y-3">
            <Skeleton className="h-20 rounded-lg" />
            <Skeleton className="h-20 rounded-lg" />
            <Skeleton className="h-20 rounded-lg" />
          </div>
        ) : isError ? (
          <ErrorState
            title={t("memory.error.load.title")}
            description={error?.message ?? t("memory.error.load.description")}
            onRetry={onRetry}
          />
        ) : filtered.length === 0 ? (
          <EmptyState
            icon={BrainCircuit}
            title={emptyLabel}
            action={
              <Button
                type="button"
                size="sm"
                onClick={() => setEditor({ mode: "create", scope, projectID })}
              >
                <Plus className="h-3.5 w-3.5" />
                {t("memory.actions.create")}
              </Button>
            }
          />
        ) : (
          <ul className="space-y-2">
            {filtered.map((memory) => (
              <MemoryRow
                key={memory.id}
                memory={memory}
                fmtAgo={fmtAgo}
                onEdit={() =>
                  setEditor({ mode: "edit", scope, projectID, memory })
                }
                onDelete={() => setConfirmDelete(memory)}
                onAudit={scope === "project" ? () => setAuditFor(memory) : undefined}
              />
            ))}
          </ul>
        )}
      </div>

      {editor && (
        <MemoryEditorDialog
          mode={editor.mode}
          scope={editor.scope}
          memory={editor.memory}
          pending={createMut.isPending || updateMut.isPending}
          error={(createMut.error ?? updateMut.error) as ApiError | undefined}
          onSubmit={handleEditorSubmit}
          onClose={closeEditor}
        />
      )}

      {confirmDelete && (
        <MemoryDeleteDialog
          loading={deleteMut.isPending}
          error={deleteMut.error as ApiError | undefined}
          onCancel={() => {
            setConfirmDelete(null)
            deleteMut.reset()
          }}
          onConfirm={handleConfirmDelete}
        />
      )}

      {auditFor && projectID && (
        <MemoryAuditDialog
          memory={auditFor}
          projectID={projectID}
          onClose={() => setAuditFor(null)}
        />
      )}
    </>
  )
}

// ----- list row -------------------------------------------------------------

interface MemoryRowProps {
  memory: Memory
  fmtAgo: (iso: string) => string
  onEdit: () => void
  onDelete: () => void
  /** Undefined for user-scope rows — see auditFor comment in MemoryPanelBody. */
  onAudit?: () => void
}

function MemoryRow({ memory, fmtAgo, onEdit, onDelete, onAudit }: MemoryRowProps) {
  const { t } = useTranslation("admin")
  const preview = memory.body.replace(/\s+/g, " ").trim().slice(0, 240)
  return (
    <li className="rounded-lg border border-line bg-surface px-4 py-3 transition-colors hover:border-line-strong">
      <button
        type="button"
        onClick={onEdit}
        className="flex w-full flex-col items-start gap-1.5 text-left"
      >
        <div className="flex w-full flex-wrap items-center gap-2">
          <MemoryTypeBadge type={memory.memory_type} />
          {memory.title && (
            <span className="text-base font-semibold text-fg">{memory.title}</span>
          )}
          <MemorySourceBadge source={memory.source} />
          {memory.tags.map((tag) => (
            <Badge key={tag} variant="neutral" className="font-mono text-xs">
              {tag}
            </Badge>
          ))}
        </div>
        {preview && (
          <p className="line-clamp-2 text-sm text-fg-muted">{preview}</p>
        )}
        {memory.why && (
          <p className="line-clamp-2 text-xs italic text-fg-subtle">
            Why: {memory.why}
          </p>
        )}
        <p className="text-xs text-fg-faint">
          {t("memory.row.updatedAt", { time: fmtAgo(memory.updated_at) })}
          {memory.agent_actor && (
            <>
              <span className="mx-1.5">·</span>
              {t("memory.row.agentActor", { actor: memory.agent_actor })}
            </>
          )}
        </p>
      </button>
      <div className="mt-2 flex justify-end gap-1.5">
        {onAudit && (
          <Button type="button" variant="ghost" size="sm" onClick={onAudit}>
            <Activity className="h-3.5 w-3.5" />
            {t("memory.audit.rowAction")}
          </Button>
        )}
        <Button type="button" variant="ghost" size="sm" onClick={onEdit}>
          {t("memory.row.edit")}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="text-danger hover:bg-danger-subtle hover:text-danger-emphasis"
          onClick={onDelete}
        >
          <Trash2 className="h-3.5 w-3.5" />
          {t("memory.row.delete")}
        </Button>
      </div>
    </li>
  )
}

function MemoryTypeBadge({ type }: { type: MemoryType }) {
  const { t } = useTranslation("admin")
  const variant =
    type === "feedback"
      ? "warning"
      : type === "project"
        ? "primary"
        : type === "reference"
          ? "success"
          : "neutral"
  return <Badge variant={variant}>{t(`memory.type.${type}` as never)}</Badge>
}

function MemorySourceBadge({ source }: { source: SpecSource }) {
  const { t } = useTranslation("admin")
  switch (source) {
    case "manual":
      return <Badge variant="neutral">{t("memory.source.manual")}</Badge>
    case "agent":
      return <Badge variant="primary">{t("memory.source.agent")}</Badge>
    case "import":
      return <Badge variant="success">{t("memory.source.import")}</Badge>
    case "user":
      return <Badge variant="neutral">{t("memory.source.user")}</Badge>
    default:
      return <Badge variant="warning">{source}</Badge>
  }
}

function TypeFilterChip({
  label,
  active,
  onClick,
}: {
  label: string
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        active
          ? "rounded-full bg-surface-emphasis px-3 py-1 text-xs font-medium text-white"
          : "rounded-full bg-surface-muted px-3 py-1 text-xs font-medium text-fg-muted transition-colors hover:bg-surface-muted hover:text-fg"
      }
    >
      {label}
    </button>
  )
}

// ----- editor dialog --------------------------------------------------------

interface EditorSubmitInput {
  memoryType: MemoryType
  title: string
  body: string
  why: string
  tags: string[]
}

interface MemoryEditorDialogProps {
  mode: "create" | "edit"
  scope: MemoryScope
  memory?: Memory
  pending: boolean
  error?: ApiError
  onSubmit: (input: EditorSubmitInput) => Promise<void>
  onClose: () => void
}

function MemoryEditorDialog({
  mode,
  scope,
  memory,
  pending,
  error,
  onSubmit,
  onClose,
}: MemoryEditorDialogProps) {
  const { t } = useTranslation("admin")
  const [memoryType, setMemoryType] = useState<MemoryType>(memory?.memory_type ?? "user")
  const [title, setTitle] = useState(memory?.title ?? "")
  const [body, setBody] = useState(memory?.body ?? "")
  const [why, setWhy] = useState(memory?.why ?? "")
  const [tagsText, setTagsText] = useState((memory?.tags ?? []).join(", "))

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault()
    const tags = tagsText
      .split(",")
      .map((tag) => tag.trim())
      .filter((tag) => tag.length > 0)
    await onSubmit({
      memoryType,
      title: title.trim(),
      body,
      why: why.trim(),
      tags,
    })
  }

  const titleKey =
    mode === "edit"
      ? "memory.editor.editTitle"
      : scope === "user"
        ? "memory.editor.createTitleUser"
        : "memory.editor.createTitleProject"

  return (
    <Dialog open onOpenChange={(next) => { if (!next && !pending) onClose() }}>
      <DialogContent className="max-w-2xl gap-0 p-0">
        <form onSubmit={handleSubmit}>
          <DialogHeader className="border-b border-line-muted px-5 py-4 pr-10">
            <DialogTitle className="text-sm">{t(titleKey)}</DialogTitle>
            <DialogDescription>{t("memory.editor.description")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3 px-5 py-4">
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("memory.editor.field.type")}
                <span className="ml-0.5 text-danger">*</span>
              </span>
              <select
                value={memoryType}
                onChange={(event) => setMemoryType(event.target.value as MemoryType)}
                // Backend PATCH doesn't accept memory_type changes; users
                // re-create to change type.
                disabled={mode === "edit"}
                className="block w-full rounded-md border border-line px-3 py-2 text-sm text-fg-emphasis focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-300 disabled:bg-surface-subtle disabled:text-fg-subtle"
              >
                {MEMORY_TYPES.map((type) => (
                  <option key={type} value={type}>
                    {t(`memory.type.${type}` as never)}
                  </option>
                ))}
              </select>
            </label>
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("memory.editor.field.title")}
              </span>
              <Input
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                placeholder={t("memory.editor.placeholder.title")}
                maxLength={200}
              />
            </label>
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("memory.editor.field.body")}
                <span className="ml-0.5 text-danger">*</span>
              </span>
              <textarea
                value={body}
                onChange={(event) => setBody(event.target.value)}
                placeholder={t("memory.editor.placeholder.body")}
                required
                rows={8}
                className="block w-full rounded-md border border-line px-3 py-2 font-mono text-sm leading-relaxed text-fg-emphasis placeholder:text-fg-faint focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-300"
              />
            </label>
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("memory.editor.field.why")}
              </span>
              <textarea
                value={why}
                onChange={(event) => setWhy(event.target.value)}
                placeholder={t("memory.editor.placeholder.why")}
                rows={3}
                className="block w-full rounded-md border border-line px-3 py-2 text-sm leading-relaxed text-fg-emphasis placeholder:text-fg-faint focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-300"
              />
            </label>
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("memory.editor.field.tags")}
              </span>
              <Input
                value={tagsText}
                onChange={(event) => setTagsText(event.target.value)}
                placeholder={t("memory.editor.placeholder.tags")}
              />
            </label>
            {error && (
              <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2">
                <p className="text-sm font-medium text-danger-emphasis">
                  {t("memory.editor.error.title")}
                </p>
                <p className="text-xs text-danger-emphasis">{error.message}</p>
              </div>
            )}
          </div>
          <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
            <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={pending}>
              {t("memory.editor.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={pending}>
              {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {mode === "create"
                ? t("memory.editor.submit.create")
                : t("memory.editor.submit.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ----- delete confirm -------------------------------------------------------

interface MemoryDeleteDialogProps {
  loading: boolean
  error?: ApiError
  onCancel: () => void
  onConfirm: () => void
}

function MemoryDeleteDialog({
  loading,
  error,
  onCancel,
  onConfirm,
}: MemoryDeleteDialogProps) {
  const { t } = useTranslation("admin")
  return (
    <AlertDialog open onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <div className="flex items-start gap-3">
            <div className="shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis">
              <ShieldCheck className="h-4 w-4" />
            </div>
            <div className="space-y-1.5">
              <AlertDialogTitle>{t("memory.delete.title")}</AlertDialogTitle>
              <AlertDialogDescription>{t("memory.delete.description")}</AlertDialogDescription>
              {error && <p className="text-sm text-danger-emphasis">{error.message}</p>}
            </div>
          </div>
        </AlertDialogHeader>
        <AlertDialogFooter className="flex flex-row items-center justify-end gap-2 pt-2">
          <AlertDialogCancel asChild>
            <Button variant="outline" size="sm" disabled={loading}>
              {t("memory.delete.cancel")}
            </Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button variant="destructive" size="sm" onClick={onConfirm} disabled={loading}>
              {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t("memory.delete.confirm")}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// ----- audit dialog ---------------------------------------------------------

interface MemoryAuditDialogProps {
  memory: Memory
  projectID: string
  onClose: () => void
}

function MemoryAuditDialog({ memory, projectID, onClose }: MemoryAuditDialogProps) {
  const { t } = useTranslation("admin")
  return (
    <Dialog open onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle className="text-sm">{t("memory.audit.dialogTitle")}</DialogTitle>
          <DialogDescription>{t("memory.audit.dialogDescription")}</DialogDescription>
        </DialogHeader>
        <ResourceAuditTimeline
          pid={projectID}
          targetType="memory"
          targetID={memory.id}
        />
      </DialogContent>
    </Dialog>
  )
}
