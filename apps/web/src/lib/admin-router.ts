import { useEffect, useState, useCallback } from "react"

export type AdminView =
  | "conversations"
  | "runs"
  | "artifacts"
  | "scheduled"
  | "agents"
  | "capabilities"
  | "connections"
  | "models"
  | "connectors"
  | "runtime"
  | "specs"
  | "memory"
  | "updates"
  | "members"
  | "secrets"
  | "usage"
  | "audit"
  | "settings"

export type RouteMode = "admin" | "profile"
export type ProfileView = "credentials"

const ALL_ADMIN_VIEWS: AdminView[] = [
  "conversations",
  "runs",
  "artifacts",
  "scheduled",
  "agents",
  "capabilities",
  "connections",
  "models",
  "connectors",
  "runtime",
  "specs",
  "memory",
  "updates",
  "members",
  "secrets",
  "usage",
  "audit",
  "settings",
]

interface AppRoute {
  mode: RouteMode
  view: AdminView | ProfileView | null
  entityId: string | null
  tab: string | null
  credentialKind: string | null
  /** Comma-separated kinds to prefill on MyCredentialsPage; opens N create
   * dialogs in sequence (channel-layer fallback for disabled MCP). */
  credentialPrefill: string[] | null
  returnTo: string | null
}

const PROFILE_VIEWS: ProfileView[] = ["credentials"]

function parsePrefill(raw: string | null): string[] | null {
  if (!raw) return null
  const items = raw
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter((s) => s.length > 0)
  return items.length > 0 ? items : null
}

function parseRoute(search: string): AppRoute {
  const cleaned = search.replace(/^\?+/, '?')
  const params = new URLSearchParams(cleaned)
  const profile = params.get("profile")
  if (profile && PROFILE_VIEWS.includes(profile as ProfileView)) {
    return {
      mode: "profile",
      view: profile as ProfileView,
      entityId: params.get("id"),
      tab: params.get("tab"),
      credentialKind: params.get("kind"),
      credentialPrefill: parsePrefill(params.get("prefill")),
      returnTo: params.get("returnTo"),
    }
  }

  const v = params.get("admin")
  const view = v && ALL_ADMIN_VIEWS.includes(v as AdminView) ? (v as AdminView) : null
  const entityId = params.get("id")
  const tab = params.get("tab")
  return { mode: "admin", view, entityId, tab, credentialKind: null, credentialPrefill: null, returnTo: null }
}

export interface NavigateOptions {
  id?: string | null
  tab?: string | null
  marketplace?: string | null
  item?: string | null
  from?: string | null
  pendingCapability?: string | null
  focus?: string | null
}

export function useAdminView(): {
  view: AdminView | null
  entityId: string | null
  tab: string | null
  navigate: (next: AdminView, opts?: NavigateOptions) => void
} {
  const { mode, view, entityId, tab } = useAppRoute()

  const navigate = useNavigateAdmin()

  return {
    view: mode === "admin" ? (view as AdminView | null) : null,
    entityId,
    tab,
    navigate,
  }
}

export function useAppRoute(): AppRoute {
  const [route, setRoute] = useState<AppRoute>(() => parseRoute(window.location.search))

  useEffect(() => {
    const handler = () => setRoute(parseRoute(window.location.search))
    window.addEventListener("popstate", handler)
    window.addEventListener("admin:navigate", handler)
    window.addEventListener("app:navigate", handler)
    return () => {
      window.removeEventListener("popstate", handler)
      window.removeEventListener("admin:navigate", handler)
      window.removeEventListener("app:navigate", handler)
    }
  }, [])

  return route
}

export function useNavigateAdmin() {
  return useCallback((next: AdminView, opts?: NavigateOptions) => {
    const url = new URL(window.location.href)
    url.searchParams.set("admin", next)
    url.searchParams.delete("profile")
    url.searchParams.delete("kind")
    url.searchParams.delete("returnTo")
    if (opts?.id) {
      url.searchParams.set("id", opts.id)
    } else {
      url.searchParams.delete("id")
    }
    if (opts?.tab) {
      url.searchParams.set("tab", opts.tab)
    } else {
      url.searchParams.delete("tab")
    }
    setOptionalParam(url, "marketplace", opts?.marketplace)
    setOptionalParam(url, "item", opts?.item)
    setOptionalParam(url, "from", opts?.from)
    setOptionalParam(url, "pendingCapability", opts?.pendingCapability)
    setOneShotParam(url, "focus", opts?.focus)
    window.history.pushState({}, "", url.toString())
    window.dispatchEvent(new Event("admin:navigate"))
  }, [])
}

export function navigateProfileCredentials(opts?: { kind?: string | null; returnTo?: string | null }) {
  const url = new URL(window.location.href)
  url.searchParams.set("profile", "credentials")
  url.searchParams.delete("admin")
  url.searchParams.delete("id")
  url.searchParams.delete("tab")
  if (opts?.kind) url.searchParams.set("kind", opts.kind)
  else url.searchParams.delete("kind")
  if (opts?.returnTo) url.searchParams.set("returnTo", opts.returnTo)
  else url.searchParams.delete("returnTo")
  window.history.pushState({}, "", url.toString())
  window.dispatchEvent(new Event("app:navigate"))
}

export function safeReturnTo(raw: string | null): string {
  if (!raw) return "/?admin=agents"
  try {
    const url = new URL(raw, window.location.origin)
    if (url.origin !== window.location.origin) return "/?admin=agents"
    return `${url.pathname}${url.search}${url.hash}` || "/?admin=agents"
  } catch {
    return "/?admin=agents"
  }
}

export function navigateAdmin(next: AdminView, opts?: NavigateOptions) {
  const url = new URL(window.location.href)
  url.searchParams.set("admin", next)
  if (opts?.id) url.searchParams.set("id", opts.id)
  else url.searchParams.delete("id")
  if (opts?.tab) url.searchParams.set("tab", opts.tab)
  else url.searchParams.delete("tab")
  setOptionalParam(url, "marketplace", opts?.marketplace)
  setOptionalParam(url, "item", opts?.item)
  setOptionalParam(url, "from", opts?.from)
  setOptionalParam(url, "pendingCapability", opts?.pendingCapability)
  setOneShotParam(url, "focus", opts?.focus)
  window.history.pushState({}, "", url.toString())
  window.dispatchEvent(new Event("admin:navigate"))
}

function setOptionalParam(url: URL, key: string, value?: string | null) {
  if (value) url.searchParams.set(key, value)
  else if (value === null) url.searchParams.delete(key)
}

function setOneShotParam(url: URL, key: string, value?: string | null) {
  if (value) url.searchParams.set(key, value)
  else url.searchParams.delete(key)
}
