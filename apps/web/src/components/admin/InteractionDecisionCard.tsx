import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Check, MessageSquare, Play, ShieldAlert, X } from "lucide-react"

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

export function InteractionDecisionCard({
  interaction,
  workspaceID,
  className,
}: {
  interaction: AgentInteraction
  workspaceID: string
  className?: string
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

  return (
    <article
      className={cn("flex min-w-0 flex-col gap-5 p-5 sm:p-6", className)}
      data-testid="interaction-card"
      data-interaction-kind={interaction.kind}
      data-request-id={interaction.request_id}
    >
      <div>
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <Badge variant={interaction.kind === "permission" ? "warning" : "primary"}>
            {t(`approvals.kind.${interaction.kind === "permission" ? "permission" : "userChoice"}`)}
          </Badge>
          <Badge variant={pending ? "warning" : "neutral"}>
            {t(`approvals.status.${interaction.status}`)}
          </Badge>
        </div>
        <h2 className="break-words text-lg font-semibold text-fg">
          {interaction.kind === "permission"
            ? String(
                interaction.request.resource ||
                  interaction.request.action ||
                  t("approvals.kind.permission"),
              )
            : firstInteractionQuestion(interaction)?.question}
        </h2>
        {interaction.request.detail ? (
          <p className="mt-2 whitespace-pre-wrap break-words text-sm text-fg-muted">
            {String(interaction.request.detail)}
          </p>
        ) : null}
      </div>

      <dl className="grid gap-3 rounded-lg bg-surface-subtle p-4 text-sm sm:grid-cols-2">
        <Meta label={t("approvals.detail.agent")} value={interaction.agent_name || "—"} />
        <Meta
          label={t("approvals.detail.conversation")}
          value={interaction.conversation_title || interaction.conversation_id}
        />
        <Meta label={t("approvals.detail.createdAt")} value={fmtAgo(interaction.created_at)} />
        <Meta label={t("approvals.detail.expiresIn")} value={fmtUntil(interaction.expires_at)} />
      </dl>

      {interaction.kind === "permission" ? (
        <pre className="max-h-52 overflow-y-auto whitespace-pre-wrap break-all rounded-lg border border-line bg-surface-subtle p-3 text-xs text-fg-muted">
          {JSON.stringify(interaction.request.payload ?? {}, null, 2)}
        </pre>
      ) : (
        <div className="space-y-5">
          {questions.map((question, index) => {
            const key = questionKey(question, index)
            const selected = answers[key] ?? []
            return (
              <fieldset key={key} disabled={!pending || resolve.isPending} className="space-y-2">
                <legend className="text-sm font-semibold text-fg">
                  {question.header ? `${question.header} · ` : ""}
                  {question.question}
                </legend>
                {question.options.map((option) => (
                  <label
                    key={option.label}
                    className="flex cursor-pointer gap-3 rounded-lg border border-line px-3 py-2.5 hover:bg-surface-muted"
                  >
                    <input
                      type={question.multi_select ? "checkbox" : "radio"}
                      name={`${interaction.id}:${key}`}
                      checked={selected.includes(option.label)}
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
                      <span className="block break-words text-sm font-medium text-fg">
                        {option.label}
                      </span>
                      {option.description ? (
                        <span className="block break-words text-xs text-fg-subtle">
                          {option.description}
                        </span>
                      ) : null}
                    </span>
                  </label>
                ))}
                {question.is_other !== false ? (
                  <Input
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
        <p className="break-words rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
          {resolve.error.message}
        </p>
      ) : null}
      {pending ? (
        <div className="flex flex-wrap gap-2">
          {interaction.kind === "permission" ? (
            <>
              <Button onClick={() => submit({ approved: true })} disabled={resolve.isPending}>
                <Check className="h-4 w-4" />
                {t("approvals.actions.allowOnce")}
              </Button>
              <Button
                variant="outline"
                onClick={() => submit({ approved: false })}
                disabled={resolve.isPending}
              >
                <X className="h-4 w-4" />
                {t("approvals.actions.deny")}
              </Button>
            </>
          ) : (
            <>
              <Button onClick={submitChoice} disabled={resolve.isPending || !hasAllAnswers}>
                <Check className="h-4 w-4" />
                {t("approvals.actions.submitAnswers")}
              </Button>
              <Button
                variant="outline"
                onClick={() => submit({ cancelled: true, note: "cancelled by user" })}
                disabled={resolve.isPending}
              >
                <X className="h-4 w-4" />
                {t("approvals.actions.cancel")}
              </Button>
            </>
          )}
        </div>
      ) : (
        <div className="flex items-start gap-2 rounded-lg border border-line bg-surface-subtle px-3 py-3 text-sm text-fg-muted">
          <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
          {t("approvals.detail.alreadyDecided")}
        </div>
      )}
      <div className="flex flex-wrap gap-2 border-t border-line pt-4">
        <Button
          variant="outline"
          size="sm"
          onClick={() => navigate("runs", { id: interaction.agent_run_id })}
        >
          <Play className="h-4 w-4" />
          {t("approvals.detail.openRun")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => navigate("conversations", { id: interaction.conversation_id })}
        >
          <MessageSquare className="h-4 w-4" />
          {t("approvals.detail.openConversation")}
        </Button>
      </div>
    </article>
  )
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-fg-faint">{label}</dt>
      <dd className="mt-0.5 break-words font-medium text-fg">{value}</dd>
    </div>
  )
}

function questionKey(question: AgentInteractionQuestion, index: number) {
  return question.id || `q${index}`
}

function toggleAnswer(current: string[], value: string, multi: boolean) {
  if (!multi) return [value]
  return current.includes(value) ? current.filter((item) => item !== value) : [...current, value]
}
