/**
 * Workspace context for scoped admin API calls.
 *
 * Resolution order: URL param `?ws=<uuid>` → localStorage `parsar.ws` →
 * null (UI shows "no workspace" empty state).
 */
import { useEffect, useMemo, useState } from "react"

const WS_KEY = "parsar.ws"
const WS_PARAM = "ws"

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function isUUID(v: string | null | undefined): v is string {
  return typeof v === "string" && UUID_RE.test(v)
}

function readUUIDFromUrl(param: string): string | null {
  const cleaned = window.location.search.replace(/^\?+/, '?')
  const v = new URLSearchParams(cleaned).get(param)
  return isUUID(v) ? v : null
}

function readUUIDFromStorage(key: string): string | null {
  try {
    const v = window.localStorage.getItem(key)
    return isUUID(v) ? v : null
  } catch {
    return null
  }
}

function writeStorage(key: string, val: string | null) {
  try {
    if (val) window.localStorage.setItem(key, val)
    else window.localStorage.removeItem(key)
  } catch {
    /* ignore quota / privacy errors */
  }
}

export function getCurrentWorkspaceId(): string | null {
  return readUUIDFromUrl(WS_PARAM) ?? readUUIDFromStorage(WS_KEY)
}

function syncURLParam(param: string, val: string | null) {
  // Must rewrite the URL because `getCurrentWorkspaceId` gives URL precedence
  // over localStorage.
  try {
    const url = new URL(window.location.href)
    if (val) url.searchParams.set(param, val)
    else url.searchParams.delete(param)
    window.history.replaceState(window.history.state, "", url.toString())
  } catch {
    /* ignore — fallback to storage-only behavior */
  }
}

export function setWorkspaceId(id: string | null) {
  writeStorage(WS_KEY, id)
  syncURLParam(WS_PARAM, id)
  window.dispatchEvent(new Event("workspace:change"))
}

/**
 * React hook returning the current workspace id (reactive to URL/storage
 * changes via custom event + popstate).
 */
export function useWorkspaceId(): string | null {
  const [ws, setWs] = useState<string | null>(() => getCurrentWorkspaceId())
  useEffect(() => {
    const refresh = () => setWs(getCurrentWorkspaceId())
    window.addEventListener("popstate", refresh)
    window.addEventListener("workspace:change", refresh)
    window.addEventListener("admin:navigate", refresh)
    return () => {
      window.removeEventListener("popstate", refresh)
      window.removeEventListener("workspace:change", refresh)
      window.removeEventListener("admin:navigate", refresh)
    }
  }, [])
  return ws
}

/**
 * Whether a real workspace is currently bound. False ⇒ pages should show
 * "pick a workspace" empty state.
 */
export function useHasWorkspace(): boolean {
  const ws = useWorkspaceId()
  return useMemo(() => isUUID(ws), [ws])
}
