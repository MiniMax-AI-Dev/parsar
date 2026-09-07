export const JOIN_INTENT_KEY = "parsar.joinWorkspaceIntent"

/**
 * Where an unauthenticated visitor was headed, kept across the sign-in round
 * trip. Sign-in can leave the page in several ways — an SSO redirect, a 401
 * bounce, the password form — and only one of them can preserve the address on
 * its own, so the intent is stashed rather than inferred from where you land.
 *
 * The allowlist is the point: whatever comes back out is fed to
 * `location.replace`, so it must be a path this app owns and never an absolute
 * URL someone put in session storage.
 */
const RETURNABLE = ["/join-workspace", "/c/", "/invite/"]

function isReturnable(path: string): boolean {
  if (!path.startsWith("/") || path.startsWith("//")) return false
  return RETURNABLE.some((prefix) => path.startsWith(prefix))
}

/** Remember the current address before handing off to sign-in. */
export function stashReturnTo(path = window.location.pathname + window.location.search): void {
  try {
    if (isReturnable(path)) sessionStorage.setItem(JOIN_INTENT_KEY, path)
  } catch {
    // sessionStorage throws in private windows; the visitor re-opens the link.
  }
}

export function popPendingJoinIntent(): string | null {
  try {
    const stash = sessionStorage.getItem(JOIN_INTENT_KEY)
    if (!stash) return null
    sessionStorage.removeItem(JOIN_INTENT_KEY)
    return isReturnable(stash) ? stash : null
  } catch {
    return null
  }
}
