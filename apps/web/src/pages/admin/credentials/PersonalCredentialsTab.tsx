import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  AlertTriangle,
  Key,
  Plus,
  Search,
} from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
import { Skeleton } from "../../../components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../../components/ui/table"
import { ApiError } from "../../../lib/api-client"
import {
  useCreateMyCredential,
  useDeleteMyCredential,
  useMyCredentials,
  usePatchMyCredential,
} from "../../../lib/api-credentials"
import { useModels } from "../../../lib/api-models"
import { useMyWorkspaces } from "../../../lib/api-workspaces"
import {
  credentialKindLabel,
  useCredentialKindOptions,
} from "../../../lib/credential-kind-ui"
import type {
  UserCredential,
  UserCredentialCreateRequest,
  UserCredentialPatchRequest,
} from "../../../lib/api-types"
import { useAppRoute, safeReturnTo } from "../../../lib/admin-router"
import { useRelativeTime } from "../../../lib/relative-time"
import { useWorkspaceId } from "../../../lib/workspace"
import {
  CredentialDialog,
  DeleteCredentialDialog,
} from "./CredentialDialogs"
import {
  computeMissingCredentials,
  useCapabilitiesPerWorkspace,
} from "./shared"

interface PersonalCredentialsTabProps {
  /** Render the return-to banner (used by the `?profile=credentials`
   * deep-link entry). */
  standalone?: boolean
}

export function PersonalCredentialsTab({ standalone = false }: PersonalCredentialsTabProps) {
  const { t, i18n } = useTranslation("admin")
  const route = useAppRoute()
  const fmtAgo = useRelativeTime()
  const wsId = useWorkspaceId()
  const credentialsQ = useMyCredentials()
  const workspacesQ = useMyWorkspaces()
  const workspaces = useMemo(() => workspacesQ.data?.workspaces ?? [], [workspacesQ.data?.workspaces])
  const capabilitiesScan = useCapabilitiesPerWorkspace(workspaces)
  // Model catalog is org-global; the endpoint still needs a workspace in
  // the URL for RBAC but the response shape is workspace-independent.
  const modelsQ = useModels(wsId)
  const kindOptions = useCredentialKindOptions(wsId)
  const createMut = useCreateMyCredential()
  const patchMut = usePatchMyCredential()
  const deleteMut = useDeleteMyCredential()

  const [createOpen, setCreateOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<UserCredential | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<UserCredential | null>(null)
  const [highlightedID, setHighlightedID] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const [pendingKind, setPendingKind] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  const credentials = useMemo(() => credentialsQ.data?.credentials ?? [], [credentialsQ.data?.credentials])

  const requestedKind = standalone ? route.credentialKind : null
  const initialPrefill = standalone ? route.credentialPrefill : null
  const returnTo = standalone ? safeReturnTo(route.returnTo) : null
  const [prefillQueue, setPrefillQueue] = useState<string[]>([])
  const prefillSeededRef = useRef(false)
  useEffect(() => {
    if (!standalone) return
    if (prefillSeededRef.current) return
    if (initialPrefill && initialPrefill.length > 0) {
      prefillSeededRef.current = true
      setPrefillQueue(initialPrefill)
      if (typeof window !== "undefined" && window.history?.replaceState) {
        try {
          const url = new URL(window.location.href)
          if (url.searchParams.has("prefill")) {
            url.searchParams.delete("prefill")
            const next = url.pathname + (url.searchParams.toString() ? `?${url.searchParams}` : "") + url.hash
            window.history.replaceState(window.history.state, "", next)
          }
        } catch {
          // URL parse failure: the ref guard above keeps the queue
          // from re-seeding.
        }
      }
    }
  }, [standalone, initialPrefill])

  // Priority: prefill queue head (channel-layer multi-kind) → ?kind=
  // single-kind → user-clicked pending kind.
  const pendingPrefillKind = prefillQueue[0] ?? requestedKind ?? pendingKind

  useEffect(() => {
    if (pendingPrefillKind && !credentialsQ.isLoading) setCreateOpen(true)
  }, [pendingPrefillKind, credentialsQ.isLoading])

  useEffect(() => {
    if (!highlightedID) return
    const timer = window.setTimeout(() => setHighlightedID(null), 3500)
    return () => window.clearTimeout(timer)
  }, [highlightedID])

  const models = useMemo(() => modelsQ.data?.models ?? [], [modelsQ.data?.models])
  const missing = useMemo(
    () => computeMissingCredentials(workspaces, capabilitiesScan.byWorkspace, models, credentials),
    [workspaces, capabilitiesScan.byWorkspace, models, credentials],
  )

  // ---- Filtered "configured" list -------------------------------------------
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return credentials
    return credentials.filter((credential) => {
      const label = credentialKindLabel(credential.kind, i18n.language, "", kindOptions.kinds).toLowerCase()
      return label.includes(needle)
    })
  }, [credentials, query, i18n.language, kindOptions.kinds])

  const loadErr = credentialsQ.error
  const isUnreachable = loadErr instanceof ApiError && loadErr.envelope.unreachable

  if (credentialsQ.isLoading) return <CredentialsLoading />
  if (loadErr) {
    return (
      <ErrorState
        title={isUnreachable ? t("myCredentials.error.unreachable.title") : t("myCredentials.error.load.title")}
        description={isUnreachable ? t("myCredentials.error.unreachable.description") : loadErr instanceof Error ? loadErr.message : t("myCredentials.error.load.hint")}
        hint={isUnreachable ? t("myCredentials.error.unreachable.hint") : t("myCredentials.error.load.hint")}
        onRetry={() => void credentialsQ.refetch()}
      />
    )
  }

  return (
    <div className="space-y-5">
      {standalone && returnTo && route.returnTo && (
        <div className="flex items-center justify-between gap-3 rounded-lg border border-line bg-surface px-4 py-3">
          <p className="text-sm text-fg-muted">{t("myCredentials.returnBanner.description")}</p>
          <Button size="sm" variant="outline" onClick={() => window.location.assign(returnTo)}>
            {t("myCredentials.returnBanner.action")}
          </Button>
        </div>
      )}

      {!capabilitiesScan.isLoading && missing.length > 0 && (
        <div className="flex items-start gap-3 rounded-lg border border-warning-border bg-warning-subtle px-4 py-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
          <div className="flex-1 space-y-1">
            <p className="text-sm font-medium text-warning-emphasis">
              {t("credentialsPage.personal.banner.title", { count: missing.length })}
            </p>
            <p className="text-sm leading-relaxed text-warning-emphasis">
              {t("credentialsPage.personal.banner.description")}
            </p>
          </div>
        </div>
      )}

      {missing.length > 0 && (
        <section className="overflow-hidden rounded-lg border border-line bg-surface">
          <div className="flex items-center justify-between gap-3 border-b border-line-muted px-4 py-3">
            <div className="flex items-center gap-2">
              <h3 className="text-base font-semibold text-fg">
                {t("credentialsPage.personal.pending.title")}
              </h3>
              <Badge variant="warning" dot>{missing.length}</Badge>
            </div>
          </div>
          <div className="divide-y divide-slate-100">
            {missing.map((row) => {
              const open = !!expanded[row.kind]
              return (
                <div key={row.kind} className="px-4 py-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <Key className="h-4 w-4 text-fg-faint" />
                        <span className="text-sm font-medium text-fg">
                          {credentialKindLabel(row.kind, i18n.language, row.kind, kindOptions.kinds)}
                        </span>
                      </div>
                      <button
                        type="button"
                        onClick={() => setExpanded((prev) => ({ ...prev, [row.kind]: !prev[row.kind] }))}
                        className="mt-1 inline-flex items-center text-sm text-fg-subtle hover:text-fg"
                      >
                        {t("credentialsPage.personal.pending.refCount", { count: row.refCount })}
                        <span className="ml-1 text-fg-faint">{open ? "▾" : "▸"}</span>
                      </button>
                    </div>
                    <Button
                      size="sm"
                      onClick={() => {
                        setPendingKind(row.kind)
                      }}
                    >
                      {t("credentialsPage.personal.pending.configure")}
                    </Button>
                  </div>
                  {open && (
                    <ul className="mt-2 ml-6 list-disc space-y-0.5 text-sm text-fg-muted">
                      {row.refs.map((ref, idx) => {
                        if (ref.source === "model") {
                          return (
                            <li key={`model:${ref.modelID}:${idx}`}>
                              <span className="text-fg-subtle">{t("credentialsPage.personal.pending.refModelPrefix")}</span>
                              <span className="mx-1.5 text-fg-faint">/</span>
                              <span>{ref.modelName}</span>
                            </li>
                          )
                        }
                        return (
                          <li key={`cap:${ref.workspaceID}:${ref.capabilityID}:${idx}`}>
                            <span className="text-fg-subtle">{ref.workspaceName}</span>
                            <span className="mx-1.5 text-fg-faint">/</span>
                            <span>{ref.capabilityName}</span>
                          </li>
                        )
                      })}
                    </ul>
                  )}
                </div>
              )
            })}
          </div>
        </section>
      )}

      {/* Configured section — render header even when empty so layout
          doesn't collapse on deletion of the last credential. */}
      <section className="overflow-hidden rounded-lg border border-line bg-surface">
        <div className="flex items-center justify-between gap-3 border-b border-line-muted px-4 py-3">
          <div className="flex items-center gap-2">
            <h3 className="text-base font-semibold text-fg">
              {t("credentialsPage.personal.configured.title")}
            </h3>
            <span className="text-sm text-fg-faint">{credentials.length}</span>
          </div>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-3.5 w-3.5" />
            {t("credentialsPage.personal.add")}
          </Button>
        </div>

        {credentials.length >= 5 && (
          <div className="relative border-b border-line-muted px-4 py-2">
            <Search className="pointer-events-none absolute left-7 top-4 h-4 w-4 text-fg-faint" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("myCredentials.search.placeholder")}
              className="pl-9"
            />
          </div>
        )}

        {credentials.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={Key}
              title={t("credentialsPage.personal.configured.empty.title")}
              description={t("credentialsPage.personal.configured.empty.description")}
              action={
                <Button size="sm" onClick={() => setCreateOpen(true)}>
                  <Plus className="h-3.5 w-3.5" />
                  {t("credentialsPage.personal.add")}
                </Button>
              }
            />
          </div>
        ) : filtered.length === 0 ? (
          <div className="p-4">
            <EmptyState icon={Search} title={t("myCredentials.emptyFiltered.title")} description={t("myCredentials.emptyFiltered.description")} />
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("myCredentials.table.kind")}</TableHead>
                <TableHead>{t("myCredentials.table.lastUsed")}</TableHead>
                <TableHead className="text-right">{t("myCredentials.table.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((credential) => (
                <TableRow key={credential.id} className={highlightedID === credential.id ? "bg-success-subtle/70" : undefined}>
                  <TableCell>
                    <div className="font-medium text-fg">
                      {credentialKindLabel(credential.kind, i18n.language, t("myCredentials.kind.unknown"), kindOptions.kinds)}
                    </div>
                    <div className="mt-0.5 text-xs text-fg-faint">
                      {t("myCredentials.table.createdAt", { date: new Date(credential.created_at).toLocaleDateString() })}
                    </div>
                  </TableCell>
                  <TableCell className="text-sm text-fg-subtle">{fmtAgo(credential.last_used_at)}</TableCell>
                  <TableCell className="text-right">
                    <div className="inline-flex items-center gap-1.5">
                      <Button variant="ghost" size="sm" onClick={() => setEditTarget(credential)}>
                        {t("myCredentials.actions.edit")}
                      </Button>
                      <Button variant="ghost" size="sm" className="text-danger-emphasis hover:text-danger-emphasis" onClick={() => setDeleteTarget(credential)}>
                        {t("myCredentials.actions.delete")}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </section>

      {createOpen && (
        <CredentialDialog
          // Key on the active prefill kind so when the prefill queue
          // advances React fully remounts the dialog — otherwise the
          // locked-kind label and password field carry over and the user
          // submits kind #2's value under kind #1's label.
          key={pendingPrefillKind ?? "__no-prefill"}
          mode="create"
          initialKind={pendingPrefillKind}
          onClose={() => {
            setCreateOpen(false)
            createMut.reset()
            setPendingKind(null)
            if (prefillQueue.length > 0) setPrefillQueue([])
          }}
          onSubmit={async (body) => {
            const created = await createMut.mutateAsync(body as UserCredentialCreateRequest)
            setHighlightedID(created.id)
            createMut.reset()
            setPendingKind(null)
            if (prefillQueue.length > 1) {
              setPrefillQueue(prefillQueue.slice(1))
            } else {
              setPrefillQueue([])
              setCreateOpen(false)
            }
          }}
          pending={createMut.isPending}
          error={createMut.error as ApiError | undefined}
        />
      )}

      {editTarget && (
        <CredentialDialog
          mode="edit"
          credential={editTarget}
          onClose={() => {
            setEditTarget(null)
            patchMut.reset()
          }}
          onSubmit={async (body) => {
            const updated = await patchMut.mutateAsync({ id: editTarget.id, body: body as UserCredentialPatchRequest })
            setHighlightedID(updated.id)
            setEditTarget(null)
            patchMut.reset()
          }}
          pending={patchMut.isPending}
          error={patchMut.error as ApiError | undefined}
        />
      )}

      {deleteTarget && (
        <DeleteCredentialDialog
          target={deleteTarget}
          onCancel={() => {
            setDeleteTarget(null)
            deleteMut.reset()
          }}
          onConfirm={async () => {
            await deleteMut.mutateAsync(deleteTarget.id)
            setDeleteTarget(null)
            deleteMut.reset()
          }}
          pending={deleteMut.isPending}
          error={deleteMut.error as ApiError | undefined}
        />
      )}
    </div>
  )
}

function CredentialsLoading() {
  return (
    <div className="space-y-2 rounded-lg border border-line bg-surface p-4">
      {Array.from({ length: 4 }).map((_, idx) => (
        <Skeleton key={idx} className="h-10 w-full" />
      ))}
    </div>
  )
}
