import type { ConversationTimelineMessage } from "../../lib/api-types"

export function stringMeta(metadata: Record<string, unknown> | undefined, key: string): string {
  if (!metadata) return ""
  const value = key.includes(".")
    ? key
        .split(".")
        .reduce<unknown>(
          (acc, part) =>
            acc && typeof acc === "object" ? (acc as Record<string, unknown>)[part] : undefined,
          metadata,
        )
    : metadata[key]
  return typeof value === "string" ? value : ""
}

export function isRuntimeErrorMessage(
  messageType: string | undefined,
  metadata: Record<string, unknown> | undefined,
): boolean {
  if (messageType === "runtime_error") return true
  if (messageType !== "error") return false
  return (
    stringMeta(metadata, "kind") === "runtime_error" ||
    stringMeta(metadata, "error.source") === "runtime"
  )
}

function capabilityRuntimeDiagnosticKey(message: ConversationTimelineMessage): string {
  if (!isRuntimeErrorMessage(message.kind, message.metadata)) return ""
  let subKind =
    stringMeta(message.metadata, "sub_kind") || stringMeta(message.metadata, "payload.sub_kind")
  const capabilityID =
    stringMeta(message.metadata, "capability_id") ||
    stringMeta(message.metadata, "payload.capability_id")
  const credentialKind =
    stringMeta(message.metadata, "credential_kind") ||
    stringMeta(message.metadata, "payload.credential_kind")
  if (subKind === "capability_credential_missing" && !credentialKind) {
    subKind = "capability_unsupported"
  }
  if (!subKind.startsWith("capability_") || !capabilityID) return ""
  return `${subKind}\u0000${capabilityID}\u0000${credentialKind}`
}

export function dedupeCapabilityRuntimeDiagnostics(
  messages: ConversationTimelineMessage[],
): ConversationTimelineMessage[] {
  const seen = new Set<string>()
  const newestFirst: ConversationTimelineMessage[] = []
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    const key = capabilityRuntimeDiagnosticKey(message)
    if (key && seen.has(key)) continue
    if (key) seen.add(key)
    newestFirst.push(message)
  }
  return newestFirst.reverse()
}
