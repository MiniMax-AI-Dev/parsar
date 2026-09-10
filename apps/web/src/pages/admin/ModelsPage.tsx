/**
 * Org-global Model catalog. Each Model carries a credential_mode:
 *   - inline_secret: bound to a shared Secret
 *   - credential_ref: each user supplies their own credential from
 *     MyCredentialsPage
 */
import { useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Database, Download, Loader2, Plus } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Skeleton } from "../../components/ui/skeleton"
import { ApiError } from "../../lib/api-client"
import {
  useBackgroundTestModels,
  useBulkDeleteModels,
  useCreateModel,
  useDeleteModel,
  useModels,
  useTestModel,
  useUpdateModelInline,
} from "../../lib/api-models"
import type {
  InlineCreateModelInput,
  ModelConnectivityResult,
} from "../../lib/api-models"
import { CreateModelDialog, EditModelDialog } from "./ModelCrudDialogs"
import { BulkImportModelsDialog } from "./BulkImportModelsDialog"
import { ModelDeleteConfirmDialog, ModelDeleteResultsDialog, type ModelDeleteResult } from "./ModelDeleteDialogs"
import { ModelTestDiagnosticsDialog } from "./ModelTestDiagnosticsDialog"
import { ModelsTable } from "./ModelsTable"
import type { Model } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useSecrets } from "../../lib/api-secrets"
import { useCredentialKindsQuery } from "./capabilities/api"
import { useAuth } from "../../lib/auth-context"
import { useToast } from "../../components/ui/toast"
import { FilterGroup, FilterMenu, FilterOption } from "../../components/ui/filter-menu"

/* --- Loading skeleton ---------------------------------------------------- */

function ModelsLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3.5 w-3.5 rounded-full" />
          <Skeleton className="h-3.5 w-3.5" />
          <Skeleton className="h-3 w-40" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-24" />
        </div>
      ))}
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
  const backgroundTestMut = useBackgroundTestModels(wsId)

  const [createOpen, setCreateOpen] = useState(false)
  const [bulkImportOpen, setBulkImportOpen] = useState(false)
  // Pre-filled values from "duplicate"; null = empty Create dialog.
  const [duplicateInitial, setDuplicateInitial] =
    useState<InlineCreateModelInput | null>(null)
  const [editModel, setEditModel] = useState<Model | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Model | null>(null)
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false)
  const bulkDeleteButtonRef = useRef<HTMLButtonElement>(null)
  const [deleteResult, setDeleteResult] = useState<ModelDeleteResult | null>(null)
  const { show } = useToast()
  const [selectedModelIDs, setSelectedModelIDs] = useState<Set<string>>(() => new Set())
  const [backgroundTestingIDs, setBackgroundTestingIDs] = useState<Set<string>>(() => new Set())
  const [testResult, setTestResult] = useState<{
    modelID: string
    data: ModelConnectivityResult
  } | null>(null)
  const [ownership, setOwnership] = useState<OwnershipFilter>("all")

  function refresh() {
    void modelsQ.refetch()
    void secretsQ.refetch()
    void kindsQ.refetch()
  }

  const allModels = useMemo(() => modelsQ.data?.models ?? [], [modelsQ.data?.models])
  const mineCount = useMemo(
    () => (currentUserID ? allModels.filter((m) => m.created_by === currentUserID).length : 0),
    [allModels, currentUserID],
  )
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
  const testingModelIDs = useMemo(() => {
    const ids = new Set(backgroundTestingIDs)
    if (testMut.isPending && typeof testMut.variables === "string") {
      ids.add(testMut.variables)
    }
    return ids
  }, [backgroundTestingIDs, testMut.isPending, testMut.variables])

  function performTest(m: Model) {
    testMut.mutate(m.id, {
      onSuccess: (data) => setTestResult({ modelID: m.id, data }),
      onError: (e) => {
        const message = e instanceof Error ? e.message : String(e)
        setTestResult({
          modelID: m.id,
          data: { supported: false, success: false, latency_ms: 0, error: message },
        })
      },
    })
  }

  function startBackgroundTests(models: Model[]) {
    const ids = models.map((model) => model.id).filter(Boolean)
    if (ids.length === 0) return
    setBackgroundTestingIDs(new Set(ids))
    backgroundTestMut.mutate({
      modelIDs: ids,
      onModelSettled: (modelID) => {
        setBackgroundTestingIDs((current) => {
          const next = new Set(current)
          next.delete(modelID)
          return next
        })
      },
    }, {
      onSettled: () => setBackgroundTestingIDs(new Set()),
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
        const failed = result.failed ?? []
        if (failed.length > 0) {
          setDeleteResult({
            workspaceID: wsId,
            data: result,
            names: Object.fromEntries(selectedModels.map((model) => [model.id, model.name])),
          })
        } else {
          show(t("models.bulkDelete.resultSummary", { deleted: result.deleted.length, failed: 0 }))
        }
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

  const pageTitle = t("models.page.title")
  const hasModels = allModels.length > 0

  return (
    <AdminLayout activeMenu="models" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={pageTitle}
          subtitleFor="models.page.title"
          action={
            <>
              {/* A filter, not a navigation control: every other ledger page
                  narrows itself with `FilterMenu`, and the action slot is for
                  what you can do here. */}
              {hasModels && (
                <FilterMenu
                  label={t("models.ownershipFilter.label")}
                  summary={ownership === "all" ? null : t(`models.ownership.${ownership}`)}
                >
                  <FilterGroup value={ownership} onValueChange={(v) => setOwnership(v as OwnershipFilter)}>
                    <FilterOption value="all" label={t("models.ownership.all")} count={allModels.length} />
                    {/* Only when we know who you are — without a user id these
                        two cannot filter anything, and the old tabs at least
                        disabled themselves. */}
                    {currentUserID && (
                      <>
                        <FilterOption value="mine" label={t("models.ownership.mine")} count={mineCount} />
                        <FilterOption value="others" label={t("models.ownership.others")} count={allModels.length - mineCount} />
                      </>
                    )}
                  </FilterGroup>
                </FilterMenu>
              )}
              {hasModels && (
                <Button
                  variant="outline"
                  onClick={() => setSelectedModelIDs((current) => new Set([...current, ...filteredModels.map((model) => model.id)]))}
                  disabled={bulkDeleteMut.isPending || filteredModels.every((model) => selectedModelIDs.has(model.id))}
                >
                  {t("models.actions.selectAll")}
                </Button>
              )}
              <Button variant="outline" onClick={() => setBulkImportOpen(true)} disabled={!wsId}>
                <Download strokeWidth={1.5} aria-hidden="true" />
                {t("models.actions.importModels")}
              </Button>
              <Button onClick={() => setCreateOpen(true)} disabled={!wsId}>
                <Plus strokeWidth={1.5} aria-hidden="true" />
                {t("models.actions.addModel")}
              </Button>
            </>
          }
        />

        {!wsId ? (
          <div className="px-6"><ScopeRequiredState scope="workspace" resourceName={pageTitle} /></div>
        ) : modelsQ.isLoading ? (
          <ModelsLoadingSkeleton />
        ) : err ? (
          <div className="px-6 pt-6">
            <ErrorState
              title={isUnreachable ? t("models.loadError.unreachable.title") : t("models.loadError.title")}
              description={isUnreachable ? t("models.loadError.unreachable.description") : t("models.loadError.description")}
              detail={!isUnreachable && err instanceof Error ? err.message : undefined}
              hint={isUnreachable ? t("models.loadError.unreachable.hint") : t("models.loadError.hint")}
              onRetry={refresh}
            />
          </div>
        ) : !hasModels ? (
          <EmptyState icon={Database} title={t("models.empty.title")} description={t("models.empty.description")} />
        ) : (
          <ModelsTable
            data={filteredModels}
            selectedIDs={selectedModelIDs}
            testingModelIDs={testingModelIDs}
            currentUserID={currentUserID}
            isAdmin={isAdmin}
            onToggleModel={toggleModelSelection}
            onRequestEdit={(m) => setEditModel(m)}
            onRequestDelete={(m) => setConfirmDelete(m)}
            onRequestDuplicate={performDuplicate}
            onTest={performTest}
          />
        )}

        {selectedModels.length > 0 && (
          <div className="flex h-10 shrink-0 items-center gap-3 border-t border-line px-4 text-xs text-fg-muted">
            <span className="tabular-nums">
              {t("models.bulkDelete.selectedCount", { count: selectedModels.length })}
            </span>
            <div className="ml-auto flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => setSelectedModelIDs(new Set())}
                disabled={bulkDeleteMut.isPending}
              >
                {tc("actions.cancel")}
              </Button>
              <Button
                size="sm"
                variant="destructive"
                ref={bulkDeleteButtonRef}
                onClick={() => setConfirmBulkDelete(true)}
                disabled={bulkDeleteMut.isPending}
              >
                {bulkDeleteMut.isPending && <Loader2 className="animate-spin" />}
                {t("models.bulkDelete.deleteSelected")}
              </Button>
            </div>
          </div>
        )}

      </div>

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
        onImported={startBackgroundTests}
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

      <ModelDeleteConfirmDialog
        open={!!confirmDelete}
        title={t("models.delete.title", { name: confirmDelete?.name ?? "" })}
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

      <ModelDeleteConfirmDialog
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

      <ModelDeleteResultsDialog
        result={deleteResult}
        onClose={() => setDeleteResult(null)}
        onCloseAutoFocus={() => bulkDeleteButtonRef.current?.focus()}
      />

      <ModelTestDiagnosticsDialog
        open={!!testResult}
        result={testResult}
        onOpenChange={(open) => {
          if (!open) {
            setTestResult(null)
            testMut.reset()
          }
        }}
      />
    </AdminLayout>
  )
}
