import { isUUID } from "./workspace"

export interface ConversationViewState {
  agentId: string | null
  conversationId: string | null
}

const KEY_PREFIX = "parsar:conv:view:"

const EMPTY_STATE: ConversationViewState = {
  agentId: null,
  conversationId: null,
}

function storageKey(workspaceId: string): string {
  return `${KEY_PREFIX}${workspaceId}`
}

function cleanId(value: unknown): string | null {
  return typeof value === "string" && isUUID(value) ? value : null
}

function parseState(raw: string | null): ConversationViewState {
  if (!raw) return EMPTY_STATE
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    return {
      agentId: cleanId(parsed.agentId),
      conversationId: cleanId(parsed.conversationId),
    }
  } catch {
    return EMPTY_STATE
  }
}

export function readConversationViewState(workspaceId: string | null): ConversationViewState {
  if (!isUUID(workspaceId)) return EMPTY_STATE
  try {
    return parseState(window.localStorage.getItem(storageKey(workspaceId)))
  } catch {
    return EMPTY_STATE
  }
}

export function writeConversationViewState(
  workspaceId: string | null,
  patch: Partial<ConversationViewState>,
): void {
  if (!isUUID(workspaceId)) return

  const current = readConversationViewState(workspaceId)
  const next: ConversationViewState = {
    agentId: Object.hasOwn(patch, "agentId") ? cleanId(patch.agentId) : current.agentId,
    conversationId: Object.hasOwn(patch, "conversationId")
      ? cleanId(patch.conversationId)
      : current.conversationId,
  }

  try {
    if (next.agentId || next.conversationId) {
      window.localStorage.setItem(storageKey(workspaceId), JSON.stringify(next))
    } else {
      window.localStorage.removeItem(storageKey(workspaceId))
    }
  } catch {
    /* ignore quota / privacy errors */
  }
}

export function forgetConversationViewConversation(
  workspaceId: string | null,
  conversationId: string,
): void {
  const current = readConversationViewState(workspaceId)
  if (current.conversationId !== conversationId) return
  writeConversationViewState(workspaceId, { conversationId: null })
}
