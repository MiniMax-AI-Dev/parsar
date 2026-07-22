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
