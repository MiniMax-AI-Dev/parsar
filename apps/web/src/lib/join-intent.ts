export const JOIN_INTENT_KEY = "parsar.joinWorkspaceIntent"

export function popPendingJoinIntent(): string | null {
  try {
    const stash = sessionStorage.getItem(JOIN_INTENT_KEY)
    if (!stash) return null
    sessionStorage.removeItem(JOIN_INTENT_KEY)
    if (!stash.startsWith("/join-workspace")) return null
    return stash
  } catch {
    return null
  }
}
