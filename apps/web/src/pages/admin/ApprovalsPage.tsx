import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowUpRight, Inbox, Loader2 } from "lucide-react"

import { InteractionList } from "../../components/admin/InteractionList"
import { InteractionRequestDetails } from "../../components/admin/InteractionRequestDetails"
import { interactionTitle, interactionRequester, INTERACTION_STATUS_ICONS } from "../../lib/interaction-presentation"
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
  LedgerId,
} from "../../components/ui/ledger"
import { Property, PropertyList } from "../../components/ui/property-list"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon } from "../../components/ui/status-icon"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useAgentInteractions, useResolveAgentInteraction } from "../../lib/api-interactions"
import type {
  AgentInteraction,
  AgentInteractionQuestion,
  ResolveAgentInteractionRequest,
} from "../../lib/api-types"
import { interactionQuestions } from "../../lib/interaction-questions"
import { useRelativeTime, useTimeUntil } from "../../lib/relative-time"
import { useWorkspaceId } from "../../lib/workspace"

/* ------------------------------------------------------------------ */
/*  List page: the approvals ledger + decision rail                    */
/* ------------------------------------------------------------------ */

export function ApprovalsPage() {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const { entityId, navigate } = useAdminView()
  const workspaceID = useWorkspaceId()

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

  const error = pendingQ.error ?? decidedQ.error ?? expiredQ.error
  const unreachable = error instanceof ApiError && error.envelope.unreachable
  const loading = pendingQ.isLoading || decidedQ.isLoading || expiredQ.isLoading

  // Nothing opens on arrival. The inbox used to select the newest pending
  // request for you, which made it the one list that opened a rail you had not
  // asked for — every other ledger waits to be clicked, and so does this.
  const selected = entityId ? rows.find((r) => r.id === entityId) : undefined

  // The rail outlives the selection by one animation so it can play its
  // exit, whether the X, the open row, or the route closed it.
  const [railInteraction, setRailInteraction] = useState<AgentInteraction | null>(selected ?? null)
  if (selected && selected !== railInteraction) setRailInteraction(selected)

  // The row is a toggle: clicking the open one closes the rail.
  const select = (id: string | null) => {
    if (id && id !== selected?.id) navigate("approvals", { id })
    else navigate("approvals")
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
                description={unreachable ? t("approvals.loadError.unreachable.description") : t("approvals.loadError.description")}
                detail={!unreachable && error instanceof Error ? error.message : undefined}
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
            <InteractionList rows={rows} selectedID={selected?.id} label={pageTitle} onSelect={select} />
          )}
        </div>

        {railInteraction && workspaceID && (
          <InteractionRail
            interaction={railInteraction}
            workspaceID={workspaceID}
            open={!!selected}
            onClose={() => select(null)}
            onClosed={() => setRailInteraction(null)}
          />
        )}
      </div>
    </AdminLayout>
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
  open,
  onClose,
  onClosed,
}: {
  interaction: AgentInteraction
  workspaceID: string
  open: boolean
  onClose: () => void
  onClosed: () => void
}) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const fmtAgo = useRelativeTime()
  const fmtUntil = useTimeUntil()
  const resolve = useResolveAgentInteraction(workspaceID)
  const [answers, setAnswers] = useState<Record<string, string[]>>({})
  const [custom, setCustom] = useState<Record<string, string>>({})
  // The rail stays mounted while the user moves between requests (so it
  // swaps instead of flying in again), so the half-typed answers of the
  // previous request are cleared here rather than by a remount.
  const [shownId, setShownId] = useState(interaction.id)
  if (shownId !== interaction.id) {
    setShownId(interaction.id)
    setAnswers({})
    setCustom({})
  }

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
      open={open}
      onClosed={onClosed}
      aria-label={`${agent} · ${interaction.id}`}
      data-testid="interaction-card"
      data-interaction-kind={interaction.kind}
      data-request-id={interaction.request_id}
      onClose={onClose}
      closeLabel={t("runs.detail.close")}
      header={
        <>
          <StatusIcon status={INTERACTION_STATUS_ICONS[interaction.status]} />
          <span className="shrink-0 text-base font-medium text-fg">{t(`approvals.status.${interaction.status}`)}</span>
          <LedgerId className="min-w-0 flex-1">{interaction.request_id || interaction.id}</LedgerId>
        </>
      }
      footer={
        <>
          {pending && isPermission && (
            <>
              <Button variant="outline" onClick={() => submit({ approved: false })} disabled={resolve.isPending}>
                {t("approvals.actions.deny")}
              </Button>
              <Button onClick={() => submit({ approved: true })} disabled={resolve.isPending}>
                {resolve.isPending && <Loader2 className="animate-spin" />}
                {t("approvals.actions.allowOnce")}
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
        <Property label={t("approvals.detail.requester")} className="h-auto min-h-7 items-start whitespace-normal py-1"><span className="min-w-0 break-words" title={`${interaction.requested_by_type || ""} ${interaction.requested_by_id || ""}`}>{interactionRequester(interaction, t)}</span></Property>
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
        {/* Who decided — not what was decided. The rail's header already
            carries the status glyph and the same word; this property used to
            repeat both, so the reader saw "⊗ 已拒绝" twice, once per pane. */}
        {!pending && interaction.resolved_by && (
          <Property label={t("approvals.detail.decidedBy")}>{interaction.resolved_by}</Property>
        )}
      </PropertyList>

      {isPermission ? (
        <div className="mt-4"><InteractionRequestDetails interaction={interaction} /></div>
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
