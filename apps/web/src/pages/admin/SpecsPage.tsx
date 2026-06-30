// Spec fragments admin — workspace-scoped CRUD. Markdown bodies render
// as <pre className="whitespace-pre-wrap"> until react-markdown lands.

import { useState } from "react"
import { useTranslation } from "react-i18next"
import { BookText, Loader2, Plus, ShieldCheck, Trash2, Upload } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
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
import { ApiError } from "../../lib/api-client"
import {
  useCreateSpecFragmentMutation,
  useDeleteSpecFragmentMutation,
  useSpecFragmentsQuery,
  useUpdateSpecFragmentMutation,
  type SpecFragment,
  type SpecSource,
} from "../../lib/api-specs"
import { useRelativeTime } from "../../lib/relative-time"
import { useWorkspaceId } from "../../lib/workspace"
import { SpecImportDialog } from "./SpecImportDialog"

type Mode = "create" | "edit"

interface EditorState {
  mode: Mode
  fragment?: SpecFragment
}

export function SpecsPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const fmtAgo = useRelativeTime()
  const listQ = useSpecFragmentsQuery(wsId)
  const createMut = useCreateSpecFragmentMutation(wsId)
  const updateMut = useUpdateSpecFragmentMutation(wsId)
  const deleteMut = useDeleteSpecFragmentMutation(wsId)

  const [editor, setEditor] = useState<EditorState | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<SpecFragment | null>(null)
  const [importOpen, setImportOpen] = useState(false)

  const fragments = listQ.data?.fragments ?? []
  const errorObj = listQ.error as ApiError | undefined

  const closeEditor = () => {
    setEditor(null)
    createMut.reset()
    updateMut.reset()
  }

  const handleEditorSubmit = async (input: { title: string; body: string; tags: string[] }) => {
    if (!editor) return
    if (editor.mode === "create") {
      await createMut.mutateAsync(input)
    } else if (editor.fragment) {
      await updateMut.mutateAsync({ fragmentID: editor.fragment.id, body: input })
    }
    closeEditor()
  }

  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    try {
      await deleteMut.mutateAsync(confirmDelete.id)
      setConfirmDelete(null)
    } catch {
      /* error surfaces inline */
    }
  }

  return (
    <AdminLayout activeMenu="settings">
      <PageHeader
        title={t("specs.page.title")}
        action={
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!wsId}
              onClick={() => setImportOpen(true)}
            >
              <Upload className="h-3.5 w-3.5" />
              {t("specs.actions.import")}
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={!wsId}
              onClick={() => setEditor({ mode: "create" })}
            >
              <Plus className="h-3.5 w-3.5" />
              {t("specs.actions.create")}
            </Button>
          </div>
        }
      />
      <SettingsTabs active="specs" />

      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("specs.page.title")} />
      ) : listQ.isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-20 rounded-lg" />
          <Skeleton className="h-20 rounded-lg" />
          <Skeleton className="h-20 rounded-lg" />
        </div>
      ) : listQ.isError ? (
        <ErrorState
          title={t("specs.error.load.title")}
          description={errorObj?.message ?? t("specs.error.load.description")}
          onRetry={() => void listQ.refetch()}
        />
      ) : fragments.length === 0 ? (
        <EmptyState
          icon={BookText}
          title={t("specs.empty.title")}
          action={
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Button type="button" size="sm" onClick={() => setEditor({ mode: "create" })}>
                <Plus className="h-3.5 w-3.5" />
                {t("specs.actions.create")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setImportOpen(true)}
              >
                <Upload className="h-3.5 w-3.5" />
                {t("specs.actions.import")}
              </Button>
            </div>
          }
        />
      ) : (
        <ul className="space-y-2">
          {fragments.map((fragment) => (
            <FragmentRow
              key={fragment.id}
              fragment={fragment}
              fmtAgo={fmtAgo}
              onEdit={() => setEditor({ mode: "edit", fragment })}
              onDelete={() => setConfirmDelete(fragment)}
            />
          ))}
        </ul>
      )}

      {editor && wsId && (
        <EditorDialog
          mode={editor.mode}
          fragment={editor.fragment}
          pending={createMut.isPending || updateMut.isPending}
          error={(createMut.error ?? updateMut.error) as ApiError | undefined}
          onSubmit={handleEditorSubmit}
          onClose={closeEditor}
        />
      )}

      {confirmDelete && (
        <DeleteConfirmDialog
          fragment={confirmDelete}
          loading={deleteMut.isPending}
          error={deleteMut.error as ApiError | undefined}
          onCancel={() => {
            setConfirmDelete(null)
            deleteMut.reset()
          }}
          onConfirm={handleConfirmDelete}
        />
      )}

      {importOpen && wsId && (
        <SpecImportDialog workspaceID={wsId} onClose={() => setImportOpen(false)} />
      )}
    </AdminLayout>
  )
}

// ----- list row -------------------------------------------------------------

interface FragmentRowProps {
  fragment: SpecFragment
  fmtAgo: (iso: string) => string
  onEdit: () => void
  onDelete: () => void
}

function FragmentRow({ fragment, fmtAgo, onEdit, onDelete }: FragmentRowProps) {
  const { t } = useTranslation("admin")
  // Body preview: collapse whitespace to a single line and cap so the
  // list stays scannable. The full body is one click away in the editor.
  const preview = fragment.body.replace(/\s+/g, " ").trim().slice(0, 240)
  return (
    <li className="rounded-lg border border-line bg-surface px-4 py-3 transition-colors hover:border-line-strong">
      <button
        type="button"
        onClick={onEdit}
        className="flex w-full flex-col items-start gap-1.5 text-left"
      >
        <div className="flex w-full flex-wrap items-center gap-2">
          <span className="text-base font-semibold text-fg">{fragment.title}</span>
          <SourceBadge source={fragment.source} />
          {fragment.tags.map((tag) => (
            <Badge key={tag} variant="neutral" className="font-mono text-xs">
              {tag}
            </Badge>
          ))}
        </div>
        {preview && (
          <p className="line-clamp-2 text-sm text-fg-muted">{preview}</p>
        )}
        <p className="text-xs text-fg-faint">
          {t("specs.row.updatedAt", { time: fmtAgo(fragment.updated_at) })}
        </p>
      </button>
      <div className="mt-2 flex justify-end gap-1.5">
        <Button type="button" variant="ghost" size="sm" onClick={onEdit}>
          {t("specs.row.edit")}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="text-danger hover:bg-danger-subtle hover:text-danger-emphasis"
          onClick={onDelete}
        >
          <Trash2 className="h-3.5 w-3.5" />
          {t("specs.row.delete")}
        </Button>
      </div>
    </li>
  )
}

function SourceBadge({ source }: { source: SpecSource }) {
  const { t } = useTranslation("admin")
  switch (source) {
    case "manual":
      return <Badge variant="neutral">{t("specs.source.manual")}</Badge>
    case "agent":
      return <Badge variant="primary">{t("specs.source.agent")}</Badge>
    case "import":
      return <Badge variant="success">{t("specs.source.import")}</Badge>
    case "user":
      return <Badge variant="neutral">{t("specs.source.user")}</Badge>
    default:
      return <Badge variant="warning">{source}</Badge>
  }
}

// ----- editor dialog --------------------------------------------------------

interface EditorDialogProps {
  mode: Mode
  fragment?: SpecFragment
  pending: boolean
  error?: ApiError
  onSubmit: (input: { title: string; body: string; tags: string[] }) => Promise<void>
  onClose: () => void
}

function EditorDialog({ mode, fragment, pending, error, onSubmit, onClose }: EditorDialogProps) {
  const { t } = useTranslation("admin")
  const [title, setTitle] = useState(fragment?.title ?? "")
  const [body, setBody] = useState(fragment?.body ?? "")
  const [tagsText, setTagsText] = useState((fragment?.tags ?? []).join(", "))

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault()
    const tags = tagsText
      .split(",")
      .map((tag) => tag.trim())
      .filter((tag) => tag.length > 0)
    await onSubmit({ title: title.trim(), body, tags })
  }

  return (
    <Dialog open onOpenChange={(next) => { if (!next && !pending) onClose() }}>
      <DialogContent className="max-w-2xl gap-0 p-0">
        <form onSubmit={handleSubmit}>
          <DialogHeader className="border-b border-line-muted px-5 py-4 pr-10">
            <DialogTitle className="text-sm">
              {mode === "create" ? t("specs.editor.createTitle") : t("specs.editor.editTitle")}
            </DialogTitle>
            <DialogDescription>{t("specs.editor.description")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3 px-5 py-4">
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("specs.editor.field.title")}
                <span className="ml-0.5 text-danger">*</span>
              </span>
              <Input
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                placeholder={t("specs.editor.placeholder.title")}
                required
                maxLength={200}
              />
            </label>
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("specs.editor.field.body")}
                <span className="ml-0.5 text-danger">*</span>
              </span>
              <textarea
                value={body}
                onChange={(event) => setBody(event.target.value)}
                placeholder={t("specs.editor.placeholder.body")}
                required
                rows={12}
                className="block w-full rounded-md border border-line px-3 py-2 font-mono text-sm leading-relaxed text-fg-emphasis placeholder:text-fg-faint focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-300"
              />
            </label>
            <label className="block space-y-1">
              <span className="text-sm font-medium text-fg-muted">
                {t("specs.editor.field.tags")}
              </span>
              <Input
                value={tagsText}
                onChange={(event) => setTagsText(event.target.value)}
                placeholder={t("specs.editor.placeholder.tags")}
              />
            </label>
            {error && (
              <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2">
                <p className="text-sm font-medium text-danger-emphasis">
                  {t("specs.editor.error.title")}
                </p>
                <p className="text-xs text-danger-emphasis">{error.message}</p>
              </div>
            )}
          </div>
          <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
            <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={pending}>
              {t("specs.editor.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={pending}>
              {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {mode === "create" ? t("specs.editor.submit.create") : t("specs.editor.submit.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ----- delete confirm -------------------------------------------------------

interface DeleteConfirmDialogProps {
  fragment: SpecFragment
  loading: boolean
  error?: ApiError
  onCancel: () => void
  onConfirm: () => void
}

function DeleteConfirmDialog({
  fragment,
  loading,
  error,
  onCancel,
  onConfirm,
}: DeleteConfirmDialogProps) {
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
              <AlertDialogTitle>
                {t("specs.delete.title", { title: fragment.title })}
              </AlertDialogTitle>
              <AlertDialogDescription>{t("specs.delete.description")}</AlertDialogDescription>
              {error && <p className="text-sm text-danger-emphasis">{error.message}</p>}
            </div>
          </div>
        </AlertDialogHeader>
        <AlertDialogFooter className="flex flex-row items-center justify-end gap-2 pt-2">
          <AlertDialogCancel asChild>
            <Button variant="outline" size="sm" disabled={loading}>
              {t("specs.delete.cancel")}
            </Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button variant="destructive" size="sm" onClick={onConfirm} disabled={loading}>
              {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t("specs.delete.confirm")}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
