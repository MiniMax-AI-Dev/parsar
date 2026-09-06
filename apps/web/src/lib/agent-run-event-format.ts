import type { AgentRunEvent } from "./api-types"

export interface RawRunEventBlock {
  key: string
  text: string
}

function formatEvent(event: AgentRunEvent): string {
  return `#${event.sequence} ${event.event_kind}\n${JSON.stringify(event.payload ?? {}, null, 2)}`
}

export function formatRawRunEvents(events: AgentRunEvent[]): RawRunEventBlock[] {
  if (events.length > 0 && events.every((event) => event.event_kind === "message.delta")) {
    const first = events[0]
    const last = events[events.length - 1]
    const sequence =
      first.sequence === last.sequence
        ? `#${first.sequence}`
        : `#${first.sequence}-${last.sequence}`
    const payload = {
      ...last.payload,
      delta: events.map((event) => String(event.payload?.delta ?? "")).join(""),
    }

    return [
      {
        key: first.id,
        text: `${sequence} message.delta\n${JSON.stringify(payload, null, 2)}`,
      },
    ]
  }

  return events.map((event) => ({ key: event.id, text: formatEvent(event) }))
}
