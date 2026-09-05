import { useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowUpRight, Check, X } from "lucide-react"

import { useAdminView } from "../../lib/admin-router"
import { useResolveAgentInteraction } from "../../lib/api-interactions"
import type {
  AgentInteraction,
  AgentInteractionQuestion,
  ResolveAgentInteractionRequest,
} from "../../lib/api-types"
import { firstInteractionQuestion, interactionQuestions } from "../../lib/interaction-questions"
import { useRelativeTime, useTimeUntil } from "../../lib/relative-time"
import { cn } from "../../lib/utils"
import { Badge } from "../ui/badge"
import { Button } from "../ui/button"
import { Input } from "../ui/input"
import { Property, PropertyList } from "../ui/property-list"

/**
 * One approval / user-input request, flat: a status badge and title, the
 * request properties, the payload or the questions, and one footer row
 * with the decision buttons left and the related links right.
 */
export function InteractionDecisionCard({
  interaction,
  workspaceID,
  className,
  hideConversationLink = false,
}: {
  interaction: AgentInteraction
  workspaceID: string
  className?: string
  /** Set when the card is already rendered inside its own conversation. */
  hideConversationLink?: boolean
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
  const hasAllAnswers =
    questions.length > 0 &&
    questions.every((question, index) => {
      const key = questionKey(question, index)
      return (answers[key]?.length ?? 0) > 0 || !!custom[key]?.trim()
    })

  const submitChoice = () => {
    const answerPayload = Object.fromEntries(
      questions.map((question, index) => {
        const key = questionKey(question, index)
        const values = [...(answers[key] ?? [])]
        if (custom[key]?.trim()) values.push(custom[key].trim())
        return [key, values]
      }),
    )
    resolve.mutate({ id: interaction.id, body: { answers: answerPayload } })
  }

  const submit = (body: ResolveAgentInteractionRequest) =>
    resolve.mutate({ id: interaction.id, body })

  const kindLabel = t(`approvals.kind.${interaction.kind === "permission" ? "permission" : "userChoice"}`)
  const title =
    interaction.kind === "permission"
      ? String(interaction.request.resource || interaction.request.action || t("approvals.kind.permission"))
      : firstInteractionQuestion(interaction)?.question

  return (
    <article
      className={cn("flex min-w-0 flex-col gap-4 text-sm", className)}
      data-testid="interaction-card"
      data-interaction-kind={interaction.kind}
      data-request-id={interaction.request_id}
    >
      <div>
        <div className="mb-1.5 flex flex-wrap items-center gap-2">
          <span className="text-xs text-fg-muted">{kindLabel}</span>
          <Badge variant={pending ? "warning" : "neutral"} dot pulse={pending}>
            {t(`approvals.status.${interaction.status}`)}
          </Badge>
        </div>
        <h2 className="break-words text-sm font-medium text-fg">{title}</h2>
        {interaction.request.detail ? (
          <p className="mt-1 whitespace-pre-wrap break-words text-sm text-fg">
            {String(interaction.request.detail)}
          </p>
        ) : null}
      </div>

      <PropertyList>
        <Property label={t("approvals.detail.agent")}>{interaction.agent_name || "—"}</Property>
        <Property label={t("approvals.detail.conversation")}>
          <span className="truncate" title={interaction.conversation_title || interaction.conversation_id}>
            {interaction.conversation_title || interaction.conversation_id}
          </span>
        </Property>
        <Property label={t("approvals.detail.createdAt")}>{fmtAgo(interaction.created_at)}</Property>
        <Property label={t("approvals.detail.expiresIn")}>{fmtUntil(interaction.expires_at)}</Property>
      </PropertyList>

      {interaction.kind === "permission" ? (
        <pre className="m-0 max-h-52 overflow-y-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
          {JSON.stringify(interaction.request.payload ?? {}, null, 2)}
        </pre>
      ) : (
        <div className="space-y-4">
          {questions.map((question, index) => {
            const key = questionKey(question, index)
            const selected = answers[key] ?? []
            return (
              <fieldset key={key} disabled={!pending || resolve.isPending} className="m-0 min-w-0 border-0 p-0">
                <legend className="mb-1 text-sm font-medium text-fg">
                  {question.header ? `${question.header} · ` : ""}
                  {question.question}
                </legend>
                <div className="border-t border-line">
                  {question.options.map((option) => (
                    <label
                      key={option.label}
                      className="flex min-h-8 cursor-pointer items-start gap-2 border-b border-line py-1.5 hover:app-hover"
                    >
                      <input
                        type={question.multi_select ? "checkbox" : "radio"}
                        name={`${interaction.id}:${key}`}
                        checked={selected.includes(option.label)}
                        className="mt-0.5 h-3.5 w-3.5 shrink-0 accent-accent"
                        onChange={() => {
                          setAnswers((current) => ({
                            ...current,
                            [key]: toggleAnswer(selected, option.label, !!question.multi_select),
                          }))
                          if (!question.multi_select)
                            setCustom((current) => ({ ...current, [key]: "" }))
                        }}
                      />
                      <span className="min-w-0">
                        <span className="block break-words text-sm text-fg">{option.label}</span>
                        {option.description ? (
                          <span className="block break-words text-xs text-fg-muted">{option.description}</span>
                        ) : null}
                      </span>
                    </label>
                  ))}
                </div>
                {question.is_other !== false ? (
                  <Input
                    className="mt-2"
                    type={question.is_secret ? "password" : "text"}
                    autoComplete={question.is_secret ? "new-password" : undefined}
                    value={custom[key] ?? ""}
                    onChange={(event) => {
                      const value = event.target.value
                      setCustom((current) => ({ ...current, [key]: value }))
                      if (!question.multi_select && value.trim())
                        setAnswers((current) => ({ ...current, [key]: [] }))
                    }}
                    placeholder={t("approvals.questions.customAnswer")}
                  />
                ) : null}
              </fieldset>
            )
          })}
        </div>
      )}

      {resolve.error ? (
        <p className="flex items-start gap-1.5 break-words text-sm text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <span>{resolve.error.message}</span>
        </p>
      ) : null}

      {!pending && <p className="text-sm text-fg-muted">{t("approvals.detail.alreadyDecided")}</p>}

      <div className="flex flex-wrap items-center gap-2 border-t border-line pt-3">
        {pending &&
          (interaction.kind === "permission" ? (
            <>
              <Button onClick={() => submit({ approved: true })} disabled={resolve.isPending}>
                <Check strokeWidth={1.5} aria-hidden="true" />
                {t("approvals.actions.allowOnce")}
              </Button>
              <Button variant="outline" onClick={() => submit({ approved: false })} disabled={resolve.isPending}>
                <X strokeWidth={1.5} aria-hidden="true" />
                {t("approvals.actions.deny")}
              </Button>
            </>
          ) : (
            <>
              <Button onClick={submitChoice} disabled={resolve.isPending || !hasAllAnswers}>
                <Check strokeWidth={1.5} aria-hidden="true" />
                {t("approvals.actions.submitAnswers")}
              </Button>
              <Button
                variant="outline"
                onClick={() => submit({ cancelled: true, note: "cancelled by user" })}
                disabled={resolve.isPending}
              >
                <X strokeWidth={1.5} aria-hidden="true" />
                {t("approvals.actions.cancel")}
              </Button>
            </>
          ))}
        <div className="ml-auto flex items-center gap-3">
          <Button variant="link" onClick={() => navigate("runs", { id: interaction.agent_run_id })}>
            {t("approvals.detail.openRun")}
            <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
          </Button>
          {!hideConversationLink && (
            <Button
              variant="link"
              onClick={() => navigate("conversations", { id: interaction.conversation_id })}
            >
              {t("approvals.detail.openConversation")}
              <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
            </Button>
          )}
        </div>
      </div>
    </article>
  )
}

function questionKey(question: AgentInteractionQuestion, index: number) {
  return question.id || `q${index}`
}

function toggleAnswer(current: string[], value: string, multi: boolean) {
  if (!multi) return [value]
  return current.includes(value) ? current.filter((item) => item !== value) : [...current, value]
}
