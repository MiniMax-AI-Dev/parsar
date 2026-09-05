import { useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { AlertTriangle, ArrowUpRight, Inbox, Loader2 } from "lucide-react"

import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { Button } from "../../components/ui/button"
import { DetailRail, RailSection } from "../../components/ui/detail-rail"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import {
  InitialTile,
  Ledger,
  LedgerGroup,
  LedgerHeader,
  LedgerId,
  LedgerRow,
  col,
} from "../../components/ui/ledger"
import { Property, PropertyList } from "../../components/ui/property-list"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon, type StatusKind } from "../../components/ui/status-icon"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useAgentInteractions, useResolveAgentInteraction } from "../../lib/api-interactions"
import type {
  AgentInteraction,
  AgentInteractionQuestion,
  AgentInteractionStatus,
  ResolveAgentInteractionRequest,
} from "../../lib/api-types"
import { firstInteractionQuestion, interactionQuestions } from "../../lib/interaction-questions"
import { useRelativeTime, useTimeUntil } from "../../lib/relative-time"
import { useWorkspaceId } from "../../lib/workspace"

/* ------------------------------------------------------------------ */
/*  List page: the approvals ledger + decision rail                    */
/* ------------------------------------------------------------------ */

const GROUP_ORDER: AgentInteractionStatus[] = [
  "pending",
  "resolving",
  "approved",
  "answered",
  "denied",
  "cancelled",
  "expired",
]

const STATUS_ICON: Record<AgentInteractionStatus, StatusKind> = {
  pending: "queued",
  resolving: "running",
  approved: "completed",
  answered: "completed",
  denied: "failed",
  cancelled: "cancelled",
  expired: "interrupted",
}

/** status icon · request (+ kind) · agent · run id · age */
const LEDGER_COLUMNS = [col.icon(), col.title(), col.text(160, 1), col.id(132), col.age(80)]

export function ApprovalsPage() {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const { entityId, navigate } = useAdminView()
  const workspaceID = useWorkspaceId()
  const fmtAgo = useRelativeTime()

  // The API serves three status buckets; the ledger shows them as one
  // list grouped by the concrete state so a decision never changes tabs.
  const pendingQ = useAgentInteractions(workspaceID, "pending")
  const decidedQ = useAgentInteractions(workspaceID, "decided")
  const expiredQ = useAgentInteractions(workspaceID, "expired")

  const rows = useMemo(
    () => [
      ...(pendingQ.data?.interactions ?? []),
      ...(decidedQ.data?.interactions ?? []),
      ...(expiredQ.data?.interactions ?? []),
    ],
    [pendingQ.data, decidedQ.data, expiredQ.data],
  )

  const groups = useMemo(
    () =>
      GROUP_ORDER.map((status) => ({
        status,
        rows: rows.filter((r) => r.status === status),
      })).filter((g) => g.rows.length > 0),
    [rows],
  )

  const error = pendingQ.error ?? decidedQ.error ?? expiredQ.error
  const unreachable = error instanceof ApiError && error.envelope.unreachable
  const loading = pendingQ.isLoading || decidedQ.isLoading || expiredQ.isLoading

  // The newest pending request is the thing to act on; it is selected
  // until the user picks something else.
  // Closing the rail must not re-select the default row; `dismissed`
  // holds the rail shut until the user picks a row or the route changes.
  const [dismissed, setDismissed] = useState(false)
  const selected =
    (entityId ? rows.find((r) => r.id === entityId) : undefined) ??
    (entityId || dismissed ? undefined : rows.find((r) => r.status === "pending"))

  const select = (id: string | null) => {
    if (id) {
      setDismissed(false)
      navigate("approvals", { id })
    } else {
      setDismissed(true)
      navigate("approvals")
    }
  }

  // The page is titled exactly as the nav item names it (common:nav.items.approvals).
  const pageTitle = tc("nav.items.approvals")
  const englishTitle = tc("nav.items.approvals", { lng: "en-US" })

  return (
    <AdminLayout activeMenu="approvals" fullBleed>
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <PageHeader className="static mx-0 mb-0" title={pageTitle} subtitle={englishTitle !== pageTitle ? englishTitle : undefined} />

          {!workspaceID ? (
            <div className="px-6"><ScopeRequiredState scope="workspace" resourceName={pageTitle} /></div>
          ) : loading ? (
            <ApprovalsLoadingSkeleton />
          ) : error ? (
            <div className="px-6 pt-6">
              <ErrorState
                title={unreachable ? t("approvals.loadError.unreachable.title") : t("approvals.loadError.title")}
                description={
                  unreachable
                    ? t("approvals.loadError.unreachable.description")
                    : error instanceof Error
                      ? error.message
                      : t("approvals.loadError.description")
                }
                hint={unreachable ? t("approvals.loadError.unreachable.hint") : t("approvals.loadError.hint")}
                onRetry={() => {
                  void pendingQ.refetch()
                  void decidedQ.refetch()
                  void expiredQ.refetch()
                }}
              />
            </div>
          ) : rows.length === 0 ? (
            <EmptyState icon={Inbox} title={t("approvals.empty.title")} description={t("approvals.empty.description")} />
          ) : (
            <Ledger columns={LEDGER_COLUMNS} role="listbox" aria-label={pageTitle}>
              <LedgerHeader>
                <span />
                <span>{t("approvals.table.request")}</span>
                <span>{t("approvals.detail.agent")}</span>
                <span>{t("runs.table.run")}</span>
                <span className="text-right">{t("runs.table.age")}</span>
              </LedgerHeader>
              {groups.map((g) => (
                <LedgerGroup key={g.status} label={t(`approvals.status.${g.status}`)} count={g.rows.length}>
                  {g.rows.map((row) => (
                    <InteractionRow
                      key={row.id}
                      row={row}
                      selected={row.id === selected?.id}
                      statusLabel={t(`approvals.status.${row.status}`)}
                      kindLabel={t(`approvals.kind.${row.kind === "permission" ? "permission" : "userChoice"}`)}
                      title={interactionTitle(row, t)}
                      age={fmtAgo(row.created_at)}
                      onSelect={() => select(row.id)}
                    />
                  ))}
                </LedgerGroup>
              ))}
            </Ledger>
          )}
        </div>

        {selected && workspaceID && (
          <InteractionRail
            key={selected.id}
            interaction={selected}
            workspaceID={workspaceID}
            onClose={() => select(null)}
          />
        )}
      </div>
    </AdminLayout>
  )
}

function interactionTitle(row: AgentInteraction, t: TFunction<"admin">): string {
  if (row.kind === "permission") {
    return String(row.request.resource || row.request.action || t("approvals.kind.permission"))
  }
  return firstInteractionQuestion(row)?.question || t("approvals.kind.userChoice")
}

function InteractionRow({
  row,
  selected,
  statusLabel,
  kindLabel,
  title,
  age,
  onSelect,
}: {
  row: AgentInteraction
  selected: boolean
  statusLabel: string
  kindLabel: string
  title: string
  age: string
  onSelect: () => void
}) {
  const agent = row.agent_name || "—"
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onSelect()
    }
  }
  return (
    <LedgerRow selected={selected} onClick={onSelect} onKeyDown={onKeyDown}>
      <StatusIcon status={STATUS_ICON[row.status]} title={statusLabel} />
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate font-medium" title={title}>{title}</span>
        <span className="shrink-0 text-xs text-fg-muted">· {kindLabel}</span>
      </span>
      <span className="flex min-w-0 items-center gap-1.5">
        <InitialTile name={agent} />
        <span className="truncate">{agent}</span>
      </span>
      <LedgerId>{shortId(row.agent_run_id, 16)}</LedgerId>
      <span className="truncate text-right text-xs text-fg-muted">{age}</span>
    </LedgerRow>
  )
}

function ApprovalsLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3.5 w-3.5 rounded-full" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-32" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Decision rail                                                      */
/*                                                                     */
/*  The one place on the screen where a request is allowed, denied,    */
/*  answered or cancelled. Permission requests show their payload;      */
/*  questions render as hairline option rows.                           */
/* ------------------------------------------------------------------ */

function InteractionRail({
  interaction,
  workspaceID,
  onClose,
}: {
  interaction: AgentInteraction
  workspaceID: string
  onClose: () => void
}) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const fmtAgo = useRelativeTime()
  const fmtUntil = useTimeUntil()
  const resolve = useResolveAgentInteraction(workspaceID)
  const [answers, setAnswers] = useState<Record<string, string[]>>({})
  const [custom, setCustom] = useState<Record<string, string>>({})

  const questions = interactionQuestions(interaction)
  const pending = interaction.status === "pending"
  const isPermission = interaction.kind === "permission"
  const title = interactionTitle(interaction, t)
  const agent = interaction.agent_name || "—"
  const detail = interaction.request.detail ? String(interaction.request.detail) : null

  const hasAllAnswers =
    questions.length > 0 &&
    questions.every((question, index) => {
      const key = questionKey(question, index)
      return (answers[key]?.length ?? 0) > 0 || !!custom[key]?.trim()
    })

  const submit = (body: ResolveAgentInteractionRequest) => resolve.mutate({ id: interaction.id, body })

  const submitChoice = () => {
    const answerPayload = Object.fromEntries(
      questions.map((question, index) => {
        const key = questionKey(question, index)
        const values = [...(answers[key] ?? [])]
        if (custom[key]?.trim()) values.push(custom[key].trim())
        return [key, values]
      }),
    )
    submit({ answers: answerPayload })
  }

  return (
    <DetailRail
      aria-label={`${agent} · ${interaction.id}`}
      data-testid="interaction-card"
      data-interaction-kind={interaction.kind}
      data-request-id={interaction.request_id}
      onClose={onClose}
      closeLabel={t("runs.detail.close")}
      header={
        <>
          <StatusIcon status={STATUS_ICON[interaction.status]} />
          <span className="shrink-0 text-sm font-medium text-fg">{t(`approvals.status.${interaction.status}`)}</span>
          <LedgerId className="min-w-0 flex-1">{interaction.request_id || interaction.id}</LedgerId>
        </>
      }
      footer={
        <>
          {pending && isPermission && (
            <>
              <Button onClick={() => submit({ approved: true })} disabled={resolve.isPending}>
                {resolve.isPending && <Loader2 className="animate-spin" />}
                {t("approvals.actions.allowOnce")}
              </Button>
              <Button variant="outline" onClick={() => submit({ approved: false })} disabled={resolve.isPending}>
                {t("approvals.actions.deny")}
              </Button>
            </>
          )}
          {pending && !isPermission && (
            <>
              <Button onClick={submitChoice} disabled={resolve.isPending || !hasAllAnswers}>
                {resolve.isPending && <Loader2 className="animate-spin" />}
                {t("approvals.actions.submitAnswers")}
              </Button>
              <Button
                variant="outline"
                onClick={() => submit({ cancelled: true, note: "cancelled by user" })}
                disabled={resolve.isPending}
              >
                {t("approvals.actions.cancel")}
              </Button>
            </>
          )}
          <Button variant="link" className="ml-auto" onClick={() => navigate("runs", { id: interaction.agent_run_id })}>
            {t("approvals.detail.openRun")}
            <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
          </Button>
        </>
      }
    >
      <h2 className="mb-3 flex items-center gap-2 text-sm font-medium text-fg">
        <InitialTile name={agent} />
        <span className="truncate">{agent}</span>
      </h2>

      <p className="break-words text-sm text-fg">
        <span className="font-medium">{title}</span>
        <span className="text-xs text-fg-muted"> · {t(`approvals.kind.${isPermission ? "permission" : "userChoice"}`)}</span>
      </p>
      {detail && <p className="mt-1 whitespace-pre-wrap break-words text-sm text-fg">{detail}</p>}

      {resolve.error && (
        <p className="mt-3 flex items-start gap-1.5 break-words text-sm text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <span>{resolve.error.message}</span>
        </p>
      )}

      <PropertyList className="mt-3">
        <Property label={t("approvals.detail.conversation")} mono>
          <button
            type="button"
            className="truncate text-left hover:underline"
            onClick={() => navigate("conversations", { id: interaction.conversation_id })}
          >
            {interaction.conversation_title || interaction.conversation_id}
          </button>
        </Property>
        <Property label={t("approvals.detail.createdAt")}>{fmtAgo(interaction.created_at)}</Property>
        {pending && <Property label={t("approvals.detail.expiresIn")}>{fmtUntil(interaction.expires_at)}</Property>}
        {!pending && (
          <Property label={t("approvals.detail.decision")}>
            <StatusIcon status={STATUS_ICON[interaction.status]} />
            <span className="truncate">
              {t(`approvals.status.${interaction.status}`)}
              {interaction.resolved_by && <span className="text-fg-muted"> · {interaction.resolved_by}</span>}
            </span>
          </Property>
        )}
      </PropertyList>

      {isPermission ? (
        <RailSection title={t("approvals.detail.payload")}>
          <pre className="m-0 mt-1.5 max-h-60 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
            {JSON.stringify(interaction.request.payload ?? {}, null, 2)}
          </pre>
        </RailSection>
      ) : (
        questions.map((question, index) => {
          const key = questionKey(question, index)
          const selected = answers[key] ?? []
          return (
            <RailSection
              key={key}
              title={question.header ? `${question.header} · ${question.question}` : question.question}
            >
              <fieldset disabled={!pending || resolve.isPending} className="m-0 min-w-0 border-0 p-0">
                <legend className="sr-only">{question.question}</legend>
                <ul className="m-0 list-none p-0">
                  {question.options.map((option) => (
                    <li key={option.label} className="border-b border-line last:border-b-0">
                      <label className="flex min-h-8 cursor-pointer items-center gap-2 py-1.5 text-sm text-fg">
                        <input
                          type={question.multi_select ? "checkbox" : "radio"}
                          name={`${interaction.id}:${key}`}
                          className="h-3.5 w-3.5 shrink-0 accent-accent"
                          checked={selected.includes(option.label)}
                          onChange={() => {
                            setAnswers((current) => ({
                              ...current,
                              [key]: toggleAnswer(selected, option.label, !!question.multi_select),
                            }))
                            if (!question.multi_select) setCustom((current) => ({ ...current, [key]: "" }))
                          }}
                        />
                        <span className="min-w-0 flex-1 truncate">
                          {option.label}
                          {option.description && <span className="text-fg-muted"> · {option.description}</span>}
                        </span>
                      </label>
                    </li>
                  ))}
                </ul>
                {question.is_other !== false && (
                  <Input
                    type={question.is_secret ? "password" : "text"}
                    autoComplete={question.is_secret ? "new-password" : undefined}
                    value={custom[key] ?? ""}
                    onChange={(event) => {
                      const value = event.target.value
                      setCustom((current) => ({ ...current, [key]: value }))
                      if (!question.multi_select && value.trim()) setAnswers((current) => ({ ...current, [key]: [] }))
                    }}
                    placeholder={t("approvals.questions.customAnswer")}
                    className="mt-2"
                  />
                )}
              </fieldset>
            </RailSection>
          )
        })
      )}
    </DetailRail>
  )
}

/* ------------------------------------------------------------------ */
/*  Helpers                                                            */
/* ------------------------------------------------------------------ */

function questionKey(question: AgentInteractionQuestion, index: number) {
  return question.id || `q${index}`
}

function toggleAnswer(current: string[], value: string, multi: boolean) {
  if (!multi) return [value]
  return current.includes(value) ? current.filter((item) => item !== value) : [...current, value]
}

function shortId(s?: string, n = 8): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(0, n) + "…"
}
