/**
 * Org-global Model catalog. Each Model carries a credential_mode:
 *   - inline_secret: bound to a shared Secret
 *   - credential_ref: each user supplies their own credential from
 *     MyCredentialsPage
 */
import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  AlertCircle,
  CheckCircle2,
  Database,
  Download,
  Loader2,
  Plus,
  RefreshCw,
  ShieldAlert,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
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
  Tabs,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import {
  useBulkDeleteModels,
  useCreateModel,
  useDeleteModel,
  useModels,
  useTestModel,
  useUpdateModelInline,
} from "../../lib/api-models"
import type {
  BulkDeleteModelsResponse,
  InlineCreateModelInput,
  ModelConnectivityResult,
} from "../../lib/api-models"
import { CreateModelDialog, EditModelDialog } from "./ModelCrudDialogs"
import { BulkImportModelsDialog } from "./BulkImportModelsDialog"
import { ModelsTable } from "./ModelsTable"
import type { Model } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useSecrets } from "../../lib/api-secrets"
import { useCredentialKindsQuery } from "./capabilities/api"
import { useAuth } from "../../lib/auth-context"

/* --- Confirm dialog ------------------------------------------------------ */

function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  destructive,
  onConfirm,
  onCancel,
  loading,
}: {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
  loading?: boolean
}) {
  const { t } = useTranslation("common")
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
        <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5 pr-5">
          <div
            className={
              destructive
                ? "shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis"
                : "shrink-0 rounded-full bg-warning-subtle p-2 text-warning"
            }
          >
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">{title}</DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">
              {description}
            </DialogDescription>
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
          <Button variant="outline" size="sm" onClick={onCancel} disabled={loading}>
            {t("actions.cancel")}
          </Button>
          <Button
            variant={destructive ? "destructive" : "default"}
            size="sm"
            onClick={onConfirm}
            disabled={loading}
          >
            {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {confirmLabel ?? t("actions.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/* --- Test-result toast --------------------------------------------------- */

function TestResultBanner({
  result,
  onClose,
}: {
  result: { modelID: string; data: ModelConnectivityResult } | null
  onClose: () => void
}) {
  const { t } = useTranslation("admin")
  if (!result) return null
  const { data } = result
  const ok = data.success
  return (
    <div className={`fixed bottom-4 right-4 z-50 max-w-md rounded-md border shadow-md ${
      ok ? "border-success-border bg-success-subtle" : "border-danger-border bg-danger-subtle"
    }`}>
      <div className="flex items-start gap-2 px-4 py-3">
        {ok ? (
          <CheckCircle2 className="mt-0.5 h-4 w-4 text-success" />
        ) : (
          <AlertCircle className="mt-0.5 h-4 w-4 text-danger-emphasis" />
        )}
        <div className="flex-1 text-sm">
          {ok ? (
            <>
              <div className="font-medium text-success-emphasis">
                {t("models.test.success", { ms: data.latency_ms })}
              </div>
              {data.sample && (
                <div className="mt-1 text-xs text-success-emphasis/80 line-clamp-2">
                  {data.sample}
                </div>
              )}
            </>
          ) : (
            <>
              <div className="font-medium text-danger-emphasis">
                {data.supported ? t("models.test.failure") : t("models.test.unsupported")}
              </div>
              {data.error && (
                <div className="mt-1 text-xs text-danger-emphasis/80 line-clamp-3">
                  {data.error}
                </div>
              )}
            </>
          )}
        </div>
        <button
          type="button"
          onClick={onClose}
          className="text-xs text-fg-subtle hover:text-fg-muted"
        >
          ×
        </button>
      </div>
    </div>
  )
}

/* --- Loading skeleton ---------------------------------------------------- */

function ModelsLoadingSkeleton() {
  return (
    <div className="space-y-3">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-64 rounded-md" />
    </div>
  )
}

/* --- Page ---------------------------------------------------------------- */

type OwnershipFilter = "all" | "mine" | "others"

export function ModelsPage() {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const wsId = useWorkspaceId()
  const { user } = useAuth()
  const currentUserID = user?.user_id ?? null
  // No runtime org-admin flag on /me yet; backend enforces per-call so
  // non-creator clicks get 403. Button still renders (greyed via tooltip).
  const isAdmin = false

  const modelsQ = useModels(wsId)
  const secretsQ = useSecrets(wsId)
  const kindsQ = useCredentialKindsQuery(wsId)

  const createMut = useCreateModel(wsId)
  const updateMut = useUpdateModelInline(wsId)
  const deleteMut = useDeleteModel(wsId)
  const bulkDeleteMut = useBulkDeleteModels(wsId)
  const testMut = useTestModel(wsId)

  const [createOpen, setCreateOpen] = useState(false)
  const [bulkImportOpen, setBulkImportOpen] = useState(false)
  // Pre-filled values from "duplicate"; null = empty Create dialog.
  const [duplicateInitial, setDuplicateInitial] =
    useState<InlineCreateModelInput | null>(null)
  const [editModel, setEditModel] = useState<Model | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Model | null>(null)
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false)
  const [selectedModelIDs, setSelectedModelIDs] = useState<Set<string>>(() => new Set())
  const [bulkDeleteResult, setBulkDeleteResult] = useState<BulkDeleteModelsResponse | null>(null)
  const [testResult, setTestResult] = useState<{
    modelID: string
    data: ModelConnectivityResult
  } | null>(null)
  const [ownership, setOwnership] = useState<OwnershipFilter>("all")

  const refreshing = modelsQ.isFetching || secretsQ.isFetching || kindsQ.isFetching
  function refresh() {
    void modelsQ.refetch()
    void secretsQ.refetch()
    void kindsQ.refetch()
  }

  const allModels = useMemo(() => modelsQ.data?.models ?? [], [modelsQ.data?.models])
  const filteredModels = useMemo(() => {
    if (ownership === "all") return allModels
    if (!currentUserID) return allModels
    if (ownership === "mine") {
      return allModels.filter((m) => m.created_by === currentUserID)
    }
    return allModels.filter((m) => m.created_by !== currentUserID)
  }, [allModels, ownership, currentUserID])

  const secrets = secretsQ.data?.secrets ?? []
  const selectedModels = useMemo(
    () => allModels.filter((model) => selectedModelIDs.has(model.id)),
    [allModels, selectedModelIDs],
  )

  const err = modelsQ.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  function performTest(m: Model) {
    testMut.mutate(m.id, {
      onSuccess: (data) => setTestResult({ modelID: m.id, data }),
      onError: (e) => {
        const message = e instanceof Error ? e.message : String(e)
        setTestResult({
          modelID: m.id,
          data: {
            supported: false,
            success: false,
            latency_ms: 0,
            error: message,
          },
        })
      },
    })
  }

  function toggleModelSelection(modelID: string, selected: boolean) {
    setSelectedModelIDs((current) => {
      const next = new Set(current)
      if (selected) {
        next.add(modelID)
      } else {
        next.delete(modelID)
      }
      return next
    })
  }

  function toggleAllVisible(selected: boolean) {
    setSelectedModelIDs((current) => {
      const next = new Set(current)
      for (const model of filteredModels) {
        if (selected) {
          next.add(model.id)
        } else {
          next.delete(model.id)
        }
      }
      return next
    })
  }

  function performDelete() {
    if (!confirmDelete) return
    deleteMut.mutate(confirmDelete.id, {
      onSuccess: () => {
        toggleModelSelection(confirmDelete.id, false)
      },
      onSettled: () => setConfirmDelete(null),
    })
  }

  function performBulkDelete() {
    const ids = selectedModels.map((model) => model.id)
    if (ids.length === 0) return
    bulkDeleteMut.mutate(ids, {
      onSuccess: (result) => {
        setBulkDeleteResult(result)
        setSelectedModelIDs((current) => {
          const next = new Set(current)
          for (const id of result.deleted) {
            next.delete(id)
          }
          return next
        })
      },
      onSettled: () => setConfirmBulkDelete(false),
    })
  }

  /**
   * Prefill the Create dialog from an existing model. API keys aren't
   * readable, so inline_secret seeds reuse secret_id (works without a
   * re-paste); credential_ref reuses the kind code.
   */
  function performDuplicate(m: Model) {
    // Avoid stacking suffixes like "Foo (copy) (copy)".
    const suffix = t("models.copy.nameSuffix")
    const seedName = m.name.endsWith(suffix) ? m.name : `${m.name}${suffix}`
    const seed: InlineCreateModelInput = {
      name: seedName,
      provider_type: m.provider_type,
      adapter: m.adapter,
      base_url: m.base_url,
      model_key: m.model_key,
      credential_mode: m.credential_mode,
      config: (m.config ?? {}) as Record<string, unknown>,
    }
    if (m.credential_mode === "inline_secret") {
      // m.secret_id is nullable when the model is in pending state;
      // we pass it through so the dialog can fall back to its own
      // "paste a fresh key" UI gracefully (no Secret pre-selected).
      if (m.secret_id) seed.existing_secret_id = m.secret_id
    } else {
      if (m.credential_kind_code) {
        seed.credential_kind_code = m.credential_kind_code
      }
    }
    setDuplicateInitial(seed)
    setCreateOpen(true)
  }

  return (
    <AdminLayout activeMenu="models">
      <PageHeader
        title={t("models.page.title")}
        description={t("models.page.description")}
      />

      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("models.page.title")} />
      ) : modelsQ.isLoading ? (
        <ModelsLoadingSkeleton />
      ) : err ? (
        <ErrorState
          title={
            isUnreachable
              ? t("models.loadError.unreachable.title")
              : t("models.loadError.title")
          }
          description={
            isUnreachable
              ? t("models.loadError.unreachable.description")
              : err instanceof Error
              ? err.message
              : t("models.loadError.description")
          }
          hint={
            isUnreachable
              ? t("models.loadError.unreachable.hint")
              : t("models.loadError.hint")
          }
          onRetry={refresh}
        />
      ) : allModels.length === 0 ? (
        <EmptyState
          icon={Database}
          title={t("models.empty.title")}
          description={t("models.empty.description")}
          action={
            <div className="flex items-center justify-center gap-2">
              <Button size="sm" shape="pill" onClick={() => setCreateOpen(true)}>
                <Plus className="h-3.5 w-3.5" /> {t("models.actions.addModel")}
              </Button>
              <Button size="sm" variant="outline" onClick={() => setBulkImportOpen(true)}>
                <Download className="h-3.5 w-3.5" /> {t("models.actions.importModels")}
              </Button>
            </div>
          }
        />
      ) : (
        <div className="space-y-3">
          <div className="flex items-center justify-between gap-3">
            <Tabs
              value={ownership}
              onValueChange={(v) => setOwnership(v as OwnershipFilter)}
            >
              <TabsList>
                <TabsTrigger value="all">
                  {t("models.ownershipFilter.all", { count: allModels.length })}
                </TabsTrigger>
                <TabsTrigger value="mine" disabled={!currentUserID}>
                  {t("models.ownershipFilter.mine", {
                    count: allModels.filter((m) => m.created_by === currentUserID)
                      .length,
                  })}
                </TabsTrigger>
                <TabsTrigger value="others" disabled={!currentUserID}>
                  {t("models.ownershipFilter.others", {
                    count: allModels.filter((m) => m.created_by !== currentUserID)
                      .length,
                  })}
                </TabsTrigger>
              </TabsList>
            </Tabs>
            <div className="flex shrink-0 items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={refresh}
                disabled={refreshing}
              >
                {refreshing ? (
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                ) : (
                  <RefreshCw className="h-3.5 w-3.5" />
                )}
                {tc("actions.refresh")}
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setBulkImportOpen(true)}
                disabled={!wsId}
              >
                <Download className="h-3.5 w-3.5" />
                {t("models.actions.importModels")}
              </Button>
              <Button
                size="sm"
                shape="pill"
                onClick={() => setCreateOpen(true)}
                disabled={!wsId}
              >
                <Plus className="h-3.5 w-3.5" /> {t("models.actions.addModel")}
              </Button>
            </div>
          </div>

          {selectedModels.length > 0 && (
            <div className="flex items-center justify-between rounded-md border border-line bg-surface px-3 py-2 text-sm">
              <span className="text-fg-muted">
                {t("models.bulkDelete.selectedCount", { count: selectedModels.length })}
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setSelectedModelIDs(new Set())}
                  disabled={bulkDeleteMut.isPending}
                >
                  {tc("actions.cancel")}
                </Button>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => setConfirmBulkDelete(true)}
                  disabled={bulkDeleteMut.isPending}
                >
                  {bulkDeleteMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  {t("models.bulkDelete.deleteSelected")}
                </Button>
              </div>
            </div>
          )}

          <ModelsTable
            data={filteredModels}
            selectedIDs={selectedModelIDs}
            testingModelID={testMut.isPending ? (testMut.variables as string) : null}
            currentUserID={currentUserID}
            isAdmin={isAdmin}
            onToggleModel={toggleModelSelection}
            onToggleAllVisible={toggleAllVisible}
            onRequestEdit={(m) => setEditModel(m)}
            onRequestDelete={(m) => setConfirmDelete(m)}
            onRequestDuplicate={performDuplicate}
            onTest={performTest}
          />
        </div>
      )}

      <CreateModelDialog
        open={createOpen}
        secrets={secrets}
        workspaceID={wsId}
        pending={createMut.isPending}
        error={createMut.error}
        initialValues={duplicateInitial}
        onOpenChange={(v) => {
          setCreateOpen(v)
          if (!v) {
            createMut.reset()
            // Drop the duplicate seed on close so the next plain
            // "+ New model" click starts from an empty dialog.
            setDuplicateInitial(null)
          }
        }}
        onSubmit={(values) => {
          createMut.mutate(values, {
            onSuccess: () => {
              setCreateOpen(false)
              setDuplicateInitial(null)
            },
          })
        }}
      />

      <BulkImportModelsDialog
        open={bulkImportOpen}
        secrets={secrets}
        workspaceID={wsId}
        onOpenChange={setBulkImportOpen}
      />

      <EditModelDialog
        open={!!editModel}
        model={editModel}
        secrets={secrets}
        workspaceID={wsId}
        pending={updateMut.isPending}
        error={updateMut.error}
        onOpenChange={(v) => {
          if (!v) {
            setEditModel(null)
            updateMut.reset()
          }
        }}
        onSubmit={(values) => {
          if (!editModel) return
          updateMut.mutate(
            { modelID: editModel.id, values },
            { onSuccess: () => setEditModel(null) }
          )
        }}
      />

      <ConfirmDialog
        open={!!confirmDelete}
        title={t("models.delete.title", {
          name: confirmDelete?.name ?? "",
        })}
        description={t("models.delete.description")}
        confirmLabel={t("models.actions.delete")}
        destructive
        loading={deleteMut.isPending}
        onCancel={() => {
          setConfirmDelete(null)
          deleteMut.reset()
        }}
        onConfirm={performDelete}
      />

      <ConfirmDialog
        open={confirmBulkDelete}
        title={t("models.bulkDelete.title", { count: selectedModels.length })}
        description={t("models.bulkDelete.description")}
        confirmLabel={t("models.bulkDelete.deleteSelected")}
        destructive
        loading={bulkDeleteMut.isPending}
        onCancel={() => {
          setConfirmBulkDelete(false)
          bulkDeleteMut.reset()
        }}
        onConfirm={performBulkDelete}
      />

      <TestResultBanner
        result={testResult}
        onClose={() => {
          setTestResult(null)
          testMut.reset()
        }}
      />

      {bulkDeleteResult && (
        <div className="fixed bottom-4 left-4 z-50 max-w-md rounded-md border border-line bg-surface shadow-md">
          <div className="flex items-start gap-2 px-4 py-3">
            {bulkDeleteResult.failed.length === 0 ? (
              <CheckCircle2 className="mt-0.5 h-4 w-4 text-success" />
            ) : (
              <AlertCircle className="mt-0.5 h-4 w-4 text-warning" />
            )}
            <div className="flex-1 text-sm">
              <div className="font-medium text-fg">
                {t("models.bulkDelete.resultSummary", {
                  deleted: bulkDeleteResult.deleted.length,
                  failed: bulkDeleteResult.failed.length,
                })}
              </div>
              {bulkDeleteResult.failed.length > 0 && (
                <div className="mt-1 line-clamp-3 text-xs text-fg-muted">
                  {bulkDeleteResult.failed.map((failure) => failure.error).join(", ")}
                </div>
              )}
            </div>
            <button
              type="button"
              onClick={() => setBulkDeleteResult(null)}
              className="text-xs text-fg-subtle hover:text-fg-muted"
            >
              ×
            </button>
          </div>
        </div>
      )}
    </AdminLayout>
  )
}
