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
  Copy,
  Cpu,
  Database,
  Globe,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  ShieldAlert,
  Trash2,
  UserCircle,
  Zap,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
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
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import {
  useCreateModel,
  useDisableModel,
  useModels,
  useTestModel,
  useUpdateModelInline,
} from "../../lib/api-models"
import type {
  InlineCreateModelInput,
  ModelConnectivityResult,
} from "../../lib/api-models"
import { CreateModelDialog, EditModelDialog } from "./ModelCrudDialogs"
import type { Model } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useSecrets } from "../../lib/api-secrets"
import { useCredentialKindsQuery } from "./capabilities/api"
import { useAuth } from "../../lib/auth-context"

/* --- Status badges -------------------------------------------------------- */

function ModelStatusBadge({ model }: { model: Model }) {
  const { t } = useTranslation("admin")
  if (model.status === "disabled") {
    return <Badge variant="neutral">{t("models.status.disabled")}</Badge>
  }
  // Active rows can still be unusable when the credential binding is
  // missing — surface as "pending configuration" instead of "active".
  if (model.credential_mode === "inline_secret" && !model.secret_id) {
    return <Badge variant="warning" dot>{t("models.status.pending")}</Badge>
  }
  if (model.credential_mode === "credential_ref" && !model.credential_kind_code) {
    return <Badge variant="warning" dot>{t("models.status.pending")}</Badge>
  }
  return <Badge variant="success" dot>{t("models.status.active")}</Badge>
}

function CredentialModeBadge({ mode }: { mode: Model["credential_mode"] }) {
  const { t } = useTranslation("admin")
  if (mode === "inline_secret") {
    return (
      <span className="inline-flex items-center gap-1 rounded-md bg-slate-100 px-1.5 py-0.5 text-[11px] font-medium text-slate-700">
        <KeyRound className="h-3 w-3" />
        {t("models.credentialMode.shared")}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-md bg-indigo-50 px-1.5 py-0.5 text-[11px] font-medium text-indigo-700">
      <UserCircle className="h-3 w-3" />
      {t("models.credentialMode.personal")}
    </span>
  )
}

/**
 * Render the wire-protocol family in plain language. Falls back to the
 * raw provider_type code for unknown providers.
 */
function ProviderCompatibilityCell({ type }: { type: string }) {
  const { t } = useTranslation("admin")
  let label: string
  switch (type) {
    case "openai":
      label = t("models.createProvider.providerTypeLabel.openai")
      break
    case "anthropic":
      label = t("models.createProvider.providerTypeLabel.anthropic")
      break
    case "anthropic-compatible":
      label = t("models.createProvider.providerTypeLabel.anthropicCompatible")
      break
    case "google":
      label = t("models.createProvider.providerTypeLabel.google")
      break
    case "openai-compatible":
      label = t("models.createProvider.providerTypeLabel.openaiCompatible")
      break
    default:
      label = type
  }

  const Icon =
    type === "openai" || type === "openai-compatible"
      ? Globe
      : type === "anthropic" || type === "anthropic-compatible"
        ? Cpu
        : Database

  return (
    <span
      className="inline-flex max-w-full items-center gap-1.5 text-[12.5px] text-slate-700"
      title={label}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-slate-400" />
      <span className="truncate">{label}</span>
    </span>
  )
}

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
                ? "shrink-0 rounded-full bg-red-100 p-2 text-red-700"
                : "shrink-0 rounded-full bg-amber-100 p-2 text-amber-700"
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
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
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
      ok ? "border-emerald-200 bg-emerald-50" : "border-red-200 bg-red-50"
    }`}>
      <div className="flex items-start gap-2 px-4 py-3">
        {ok ? (
          <CheckCircle2 className="mt-0.5 h-4 w-4 text-emerald-700" />
        ) : (
          <AlertCircle className="mt-0.5 h-4 w-4 text-red-700" />
        )}
        <div className="flex-1 text-[12.5px]">
          {ok ? (
            <>
              <div className="font-medium text-emerald-900">
                {t("models.test.success", { ms: data.latency_ms })}
              </div>
              {data.sample && (
                <div className="mt-1 text-[11.5px] text-emerald-800/80 line-clamp-2">
                  {data.sample}
                </div>
              )}
            </>
          ) : (
            <>
              <div className="font-medium text-red-900">
                {data.supported ? t("models.test.failure") : t("models.test.unsupported")}
              </div>
              {data.error && (
                <div className="mt-1 text-[11.5px] text-red-800/80 line-clamp-3">
                  {data.error}
                </div>
              )}
            </>
          )}
        </div>
        <button
          type="button"
          onClick={onClose}
          className="text-[11px] text-slate-500 hover:text-slate-700"
        >
          ×
        </button>
      </div>
    </div>
  )
}

/* --- Models table -------------------------------------------------------- */

function ModelsTable({
  data,
  testingModelID,
  onRequestEdit,
  onRequestDisable,
  onRequestDuplicate,
  onTest,
  currentUserID,
  isAdmin,
}: {
  data: Model[]
  testingModelID: string | null
  onRequestEdit: (m: Model) => void
  onRequestDisable: (m: Model) => void
  onRequestDuplicate: (m: Model) => void
  onTest: (m: Model) => void
  currentUserID: string | null
  isAdmin: boolean
}) {
  const { t } = useTranslation("admin")

  if (data.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-slate-200 bg-white px-4 py-10 text-center text-[13px] text-slate-500">
        {t("models.empty.descriptionShort")}
      </div>
    )
  }

  return (
    <div className="overflow-hidden rounded-lg border border-slate-200 bg-white">
      <Table className="table-fixed">
        <colgroup>
          {/* name | model_key | compatibility | credential | status | actions
             Actions column gets 14% because it now hosts four icon-only
             buttons inline (test / edit / copy / disable). With the old
             10% the row contents would push the table past its container
             and trigger horizontal scroll on a regular laptop width. */}
          <col className="w-[22%]" />
          <col className="w-[26%]" />
          <col className="w-[16%]" />
          <col className="w-[12%]" />
          <col className="w-[10%]" />
          <col className="w-[14%]" />
        </colgroup>
        <TableHeader>
          <TableRow>
            <TableHead>{t("models.table.model")}</TableHead>
            <TableHead>{t("models.table.modelKey")}</TableHead>
            <TableHead>{t("models.table.compatibility")}</TableHead>
            <TableHead>{t("models.table.credentialMode")}</TableHead>
            <TableHead>{t("models.table.status")}</TableHead>
            <TableHead className="pr-3 text-right">{t("models.table.actions")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.map((m) => {
            const canEdit = isAdmin || (currentUserID && m.created_by === currentUserID)
            const isTesting = testingModelID === m.id
            const canTest = !isTesting && m.status === "active"
            const canDisable = !!canEdit && m.status === "active"
            return (
              <TableRow key={m.id}>
                <TableCell className="overflow-hidden">
                  <span
                    className="block truncate text-sm font-medium text-slate-900"
                    title={m.name}
                  >
                    {m.name}
                  </span>
                </TableCell>
                <TableCell className="overflow-hidden">
                  <span
                    className="block truncate font-mono text-[12px] text-slate-600"
                    title={m.model_key}
                  >
                    {m.model_key}
                  </span>
                </TableCell>
                <TableCell className="overflow-hidden">
                  <ProviderCompatibilityCell type={m.provider_type} />
                </TableCell>
                <TableCell>
                  <CredentialModeBadge mode={m.credential_mode} />
                </TableCell>
                <TableCell>
                  <ModelStatusBadge model={m} />
                </TableCell>
                <TableCell className="pr-3 text-right">
                  {/* Inline icon-only action row — replaces the v1 mix of
                      a labeled "测试连接" button and a ⋯ dropdown. Trash
                      icon means "disable" here (we don't hard-delete),
                      hence the title attribute spelling that out. */}
                  <div className="inline-flex items-center gap-0.5">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8"
                      onClick={() => onTest(m)}
                      disabled={!canTest}
                      title={t("models.actions.test")}
                      aria-label={t("models.actions.test")}
                    >
                      {isTesting ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        <Zap className="h-4 w-4" />
                      )}
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8"
                      onClick={() => onRequestEdit(m)}
                      disabled={!canEdit}
                      title={
                        canEdit
                          ? t("models.actions.edit")
                          : t("models.actions.editForbidden")
                      }
                      aria-label={t("models.actions.edit")}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8"
                      onClick={() => onRequestDuplicate(m)}
                      title={t("models.actions.copy")}
                      aria-label={t("models.actions.copy")}
                    >
                      <Copy className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-red-600 hover:bg-red-50 hover:text-red-700"
                      onClick={() => onRequestDisable(m)}
                      disabled={!canDisable}
                      title={
                        canEdit
                          ? t("models.actions.disable")
                          : t("models.actions.disableForbidden")
                      }
                      aria-label={t("models.actions.disable")}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
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
  const disableMut = useDisableModel(wsId)
  const testMut = useTestModel(wsId)

  const [createOpen, setCreateOpen] = useState(false)
  // Pre-filled values from "duplicate"; null = empty Create dialog.
  const [duplicateInitial, setDuplicateInitial] =
    useState<InlineCreateModelInput | null>(null)
  const [editModel, setEditModel] = useState<Model | null>(null)
  const [confirmDisable, setConfirmDisable] = useState<Model | null>(null)
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

  const allModels = modelsQ.data?.models ?? []
  const filteredModels = useMemo(() => {
    if (ownership === "all") return allModels
    if (!currentUserID) return allModels
    if (ownership === "mine") {
      return allModels.filter((m) => m.created_by === currentUserID)
    }
    return allModels.filter((m) => m.created_by !== currentUserID)
  }, [allModels, ownership, currentUserID])

  const secrets = secretsQ.data?.secrets ?? []

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

  function performDisable() {
    if (!confirmDisable) return
    disableMut.mutate(confirmDisable.id, {
      onSettled: () => setConfirmDisable(null),
    })
  }

  /**
   * Prefill the Create dialog from an existing model. API keys aren't
   * readable, so inline_secret seeds reuse secret_id (works without a
   * re-paste); credential_ref reuses the kind code.
   */
  function performDuplicate(m: Model) {
    // Avoid stacking suffixes like "Foo (副本) (副本)".
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
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="h-3.5 w-3.5" /> {t("models.actions.addModel")}
            </Button>
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
                size="sm"
                onClick={() => setCreateOpen(true)}
                disabled={!wsId}
              >
                <Plus className="h-3.5 w-3.5" /> {t("models.actions.addModel")}
              </Button>
            </div>
          </div>

          <ModelsTable
            data={filteredModels}
            testingModelID={testMut.isPending ? (testMut.variables as string) : null}
            currentUserID={currentUserID}
            isAdmin={isAdmin}
            onRequestEdit={(m) => setEditModel(m)}
            onRequestDisable={(m) => setConfirmDisable(m)}
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
            // "+ 新建模型" click starts from an empty dialog.
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
        open={!!confirmDisable}
        title={t("models.disable.title", {
          name: confirmDisable?.name ?? "",
        })}
        description={t("models.disable.description")}
        confirmLabel={t("models.actions.disable")}
        destructive
        loading={disableMut.isPending}
        onCancel={() => {
          setConfirmDisable(null)
          disableMut.reset()
        }}
        onConfirm={performDisable}
      />

      <TestResultBanner
        result={testResult}
        onClose={() => {
          setTestResult(null)
          testMut.reset()
        }}
      />
    </AdminLayout>
  )
}
