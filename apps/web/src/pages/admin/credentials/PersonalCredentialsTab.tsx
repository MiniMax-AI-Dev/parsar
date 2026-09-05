import { Fragment, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronDown, KeyRound, Pencil, Plus, Search, Trash2 } from "lucide-react"

import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import {
  Ledger,
  LedgerGroup,
  LedgerHeader,
  LedgerId,
  LedgerRow,
} from "../../../components/ui/ledger"
import { Skeleton } from "../../../components/ui/skeleton"
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
import { useAppRoute } from "../../../lib/admin-router"
import { useRelativeTime } from "../../../lib/relative-time"
import { useWorkspaceId } from "../../../lib/workspace"
import { cn } from "../../../lib/utils"
import {
  CredentialDialog,
  DeleteCredentialDialog,
} from "./CredentialDialogs"
import {
  computeMissingCredentials,
  useCapabilitiesPerWorkspace,
  type MissingCredentialRow,
} from "./shared"

interface PersonalCredentialsTabProps {
  /** Honour the `?profile=credentials` deep-link params (kind, prefill). */
  standalone?: boolean
  /** Header search term; matches the kind label. */
  query?: string
  /** Increments each time the page-level "add" button is pressed. */
  createRequest?: number
}

/** kind · code / refs · created · last used · actions */
const LEDGER_COLUMNS = "minmax(0,1fr) 176px 148px 96px 72px"

export function PersonalCredentialsTab({ standalone = false, query = "", createRequest = 0 }: PersonalCredentialsTabProps) {
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
  const [pendingKind, setPendingKind] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  const credentials = useMemo(() => credentialsQ.data?.credentials ?? [], [credentialsQ.data?.credentials])

  const requestedKind = standalone ? route.credentialKind : null
  const initialPrefill = standalone ? route.credentialPrefill : null
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

  // The page header owns the "add" button and bumps `createRequest`; any
  // bump we have not yet dismissed counts as an open (unprefilled) dialog.
  const [dismissedRequest, setDismissedRequest] = useState(createRequest)
  const headerRequestedCreate = createRequest > dismissedRequest
  const dialogOpen = createOpen || headerRequestedCreate
  const dialogKind = headerRequestedCreate && !prefillQueue[0] && !requestedKind ? null : pendingPrefillKind

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
      return label.includes(needle) || credential.kind.toLowerCase().includes(needle)
    })
  }, [credentials, query, i18n.language, kindOptions.kinds])

  const loadErr = credentialsQ.error
  const isUnreachable = loadErr instanceof ApiError && loadErr.envelope.unreachable

  if (credentialsQ.isLoading) return <CredentialsLoading />
  if (loadErr) {
    return (
      <div className="px-4 pt-4">
        <ErrorState
          title={isUnreachable ? t("myCredentials.error.unreachable.title") : t("myCredentials.error.load.title")}
          description={isUnreachable ? t("myCredentials.error.unreachable.description") : loadErr instanceof Error ? loadErr.message : t("myCredentials.error.load.hint")}
          hint={isUnreachable ? t("myCredentials.error.unreachable.hint") : t("myCredentials.error.load.hint")}
          onRetry={() => void credentialsQ.refetch()}
        />
      </div>
    )
  }

  const showPending = !capabilitiesScan.isLoading && missing.length > 0
  const kindLabelOf = (kind: string, fallback: string) => credentialKindLabel(kind, i18n.language, fallback, kindOptions.kinds)
  const createdLabel = (iso: string) => {
    const ms = Date.parse(iso)
    return Number.isNaN(ms) ? "—" : t("myCredentials.table.createdAt", { date: new Date(ms).toLocaleDateString() })
  }

  return (
    <>
      {credentials.length === 0 && !showPending ? (
        <EmptyState
          icon={KeyRound}
          title={t("credentialsPage.personal.configured.empty.title")}
          description={t("credentialsPage.personal.configured.empty.description")}
        />
      ) : (
        <Ledger columns={LEDGER_COLUMNS} role="list" aria-label={t("credentialsPage.tabs.personal")}>
          <LedgerHeader>
            <span>{t("myCredentials.table.kind")}</span>
            <span />
            <span />
            <span className="text-right">{t("myCredentials.table.lastUsed")}</span>
            <span />
          </LedgerHeader>

          {showPending && (
            <LedgerGroup label={t("credentialsPage.personal.pending.title")} count={missing.length}>
              {missing.map((row) => (
                <Fragment key={row.kind}>
                  <PendingRow
                    row={row}
                    label={kindLabelOf(row.kind, row.kind)}
                    open={!!expanded[row.kind]}
                    onToggle={() => setExpanded((prev) => ({ ...prev, [row.kind]: !prev[row.kind] }))}
                    onConfigure={() => setPendingKind(row.kind)}
                  />
                  {expanded[row.kind] && (
                    <li className="border-b border-line">
                      <ul className="m-0 list-none p-0">
                        {row.refs.map((ref, idx) => (
                          <li
                            key={ref.source === "model" ? `model:${ref.modelID}:${idx}` : `cap:${ref.workspaceID}:${ref.capabilityID}:${idx}`}
                            className="flex h-8 items-center gap-1.5 border-t border-line pl-10 pr-4 text-sm text-fg first:border-t-0"
                          >
                            <span className="text-fg-muted">
                              {ref.source === "model" ? t("credentialsPage.personal.pending.refModelPrefix") : ref.workspaceName}
                            </span>
                            <span className="text-fg-muted">/</span>
                            <span className="truncate">{ref.source === "model" ? ref.modelName : ref.capabilityName}</span>
                          </li>
                        ))}
                      </ul>
                    </li>
                  )}
                </Fragment>
              ))}
            </LedgerGroup>
          )}

          <LedgerGroup label={t("credentialsPage.personal.configured.title")} count={credentials.length}>
            {credentials.length === 0 ? (
              <li className="flex h-9 items-center border-b border-line px-4 text-sm text-fg-muted">
                {t("credentialsPage.personal.configured.empty.title")}
              </li>
            ) : filtered.length === 0 ? (
              <li>
                <EmptyState icon={Search} title={t("myCredentials.emptyFiltered.title")} description={t("myCredentials.emptyFiltered.description")} className="py-8" />
              </li>
            ) : (
              filtered.map((credential) => (
                <LedgerRow key={credential.id} role="listitem" tabIndex={-1} selected={highlightedID === credential.id}>
                  <span className="flex min-w-0 items-center gap-1.5">
                    <KeyRound className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
                    <span className="truncate font-medium">{kindLabelOf(credential.kind, t("myCredentials.kind.unknown"))}</span>
                  </span>
                  <LedgerId>{credential.kind}</LedgerId>
                  <span className="truncate text-xs text-fg-muted">{createdLabel(credential.created_at)}</span>
                  <span className="truncate text-right text-xs text-fg-muted">{fmtAgo(credential.last_used_at)}</span>
                  <RowActions>
                    <ActionIconButton icon={Pencil} label={t("myCredentials.actions.edit")} onClick={() => setEditTarget(credential)} />
                    <ActionIconButton icon={Trash2} label={t("myCredentials.actions.delete")} tone="danger" onClick={() => setDeleteTarget(credential)} />
                  </RowActions>
                </LedgerRow>
              ))
            )}
          </LedgerGroup>
        </Ledger>
      )}

      {dialogOpen && (
        <CredentialDialog
          // Key on the active prefill kind so when the prefill queue
          // advances React fully remounts the dialog — otherwise the
          // locked-kind label and password field carry over and the user
          // submits kind #2's value under kind #1's label.
          key={dialogKind ?? "__no-prefill"}
          mode="create"
          initialKind={dialogKind}
          onClose={() => {
            setCreateOpen(false)
            setDismissedRequest(createRequest)
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
              setDismissedRequest(createRequest)
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
    </>
  )
}

function PendingRow({
  row,
  label,
  open,
  onToggle,
  onConfigure,
}: {
  row: MissingCredentialRow
  label: string
  open: boolean
  onToggle: () => void
  onConfigure: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <LedgerRow role="listitem" tabIndex={-1}>
      <span className="flex min-w-0 items-center gap-1.5">
        <KeyRound className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <span className="truncate font-medium">{label}</span>
      </span>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className="inline-flex min-w-0 items-center gap-1 truncate text-left text-xs text-fg-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
      >
        <ChevronDown
          className={cn("h-3.5 w-3.5 shrink-0 transition-transform duration-200 ease-spring", !open && "-rotate-90")}
          strokeWidth={1.5}
          aria-hidden="true"
        />
        <span className="truncate">{t("credentialsPage.personal.pending.refCount", { count: row.refCount })}</span>
      </button>
      <span />
      <span />
      <RowActions>
        <ActionIconButton icon={Plus} label={t("credentialsPage.personal.pending.configure")} onClick={onConfigure} />
      </RowActions>
    </LedgerRow>
  )
}

function CredentialsLoading() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 4 }).map((_, idx) => (
        <div key={idx} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3.5 w-3.5 rounded-full" />
          <Skeleton className="h-3 w-40" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
  )
}
