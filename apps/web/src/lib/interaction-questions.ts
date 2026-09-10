import type { AgentInteraction, AgentInteractionQuestion } from "./api-types"

export function interactionQuestions(row: AgentInteraction): AgentInteractionQuestion[] {
  return Array.isArray(row.request.questions)
    ? (row.request.questions as unknown as AgentInteractionQuestion[])
    : []
}

export function firstInteractionQuestion(
  row: AgentInteraction,
): AgentInteractionQuestion | undefined {
  return interactionQuestions(row)[0]
}

export function savedQuestionAnswer(row: AgentInteraction, question: AgentInteractionQuestion, index: number) {
  if (row.status !== "answered") return undefined
  const answers = row.response?.answers
  const value = answers && typeof answers === "object" && !Array.isArray(answers)
    ? (answers as Record<string, unknown>)[question.id || `q${index}`]
    : undefined
  const values = Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []
  const labels = new Set(question.options.map((option) => option.label))
  return {
    selected: values.filter((answer) => labels.has(answer)),
    custom: values.filter((answer) => !labels.has(answer)).join(", "),
  }
}
