import { StrictMode, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AdminRouter } from './pages/admin/AdminRouter'
import { LoginPage } from './pages/LoginPage'
import { OnboardingPage } from './pages/OnboardingPage'
import {
  JoinWorkspaceLanding,
  popPendingJoinIntent,
} from './pages/JoinWorkspaceLanding'
import { InviteAcceptPage } from './pages/InviteAcceptPage'
import { bootstrapWorkspace } from './lib/bootstrap'
import { AuthProvider, useAuth } from './lib/auth-context'
import { useMyWorkspaces } from './lib/api-workspaces'
import { useTranslation } from 'react-i18next'
import './style.css'
import './i18n' // bootstrap i18next
import './i18n/types' // type-augment t() keys

// Defaults tuned for an admin UI: don't refetch aggressively on focus, keep
// cache around for snappy back/forward navigation.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      staleTime: 30_000,
      gcTime: 5 * 60_000,
    },
  },
})

// Best-effort; never blocks first render.
void bootstrapWorkspace()

function LoadingScreen({ message }: { message: string }) {
  return (
    <main className="grid min-h-screen place-items-center bg-surface text-base text-fg-subtle">
      {message}
    </main>
  )
}

// AuthedRoot decides whether the post-login user lands on the admin shell
// or onboarding. Split out so useMyWorkspaces only runs once we know there's
// a session — the endpoint is 401 without one.
function AuthedRoot() {
  const { t } = useTranslation("common")
  const wsQuery = useMyWorkspaces()

  if (wsQuery.isLoading) {
    return <LoadingScreen message={t("login.loading")} />
  }
  // The hook's API-unreachable fallback returns MOCK_WORKSPACES (non-empty),
  // so this branch fires only when the server actually responded with zero
  // memberships — the brand-new-user case post-OAuth-callback.
  if ((wsQuery.data?.workspaces.length ?? 0) === 0) {
    return <OnboardingPage />
  }
  return <AdminRouter />
}

function Root() {
  const { t } = useTranslation("common")
  const { isLoading, isAuthenticated } = useAuth()

  // Feishu rejection card → /join-workspace?id=<wsID>&from=feishu deep link.
  // Matched before the auth-aware admin shell so an unauthenticated user on
  // this URL hits the landing page directly.
  const joinWsId = parseJoinWorkspaceId()
  const inviteToken = parseInviteToken()

  // OAuth callback returns to "/" by design. If we stashed an intent before
  // the OAuth bounce, re-issue it now. Guarded on isAuthenticated to avoid a
  // redirect loop during the initial session-resolution window.
  useEffect(() => {
    if (isLoading || !isAuthenticated) return
    if (joinWsId !== null) return // already on the landing page
    const pending = popPendingJoinIntent()
    if (pending) {
      window.location.replace(pending)
    }
  }, [isLoading, isAuthenticated, joinWsId])

  if (inviteToken !== null) {
    return <InviteAcceptPage token={inviteToken} />
  }
  if (joinWsId !== null) {
    return <JoinWorkspaceLanding workspaceId={joinWsId} />
  }
  if (isLoading) {
    return <LoadingScreen message={t("login.loading")} />
  }
  if (!isAuthenticated) {
    return <LoginPage />
  }
  return <AuthedRoot />
}

// Pure URL parse (no react-router dependency) so the landing page stays
// self-contained.
function parseJoinWorkspaceId(): string | null {
  if (typeof window === "undefined") return null
  if (window.location.pathname !== "/join-workspace") return null
  const id = new URLSearchParams(window.location.search).get("id")
  return id && id.length > 0 ? id : null
}

function parseInviteToken(): string | null {
  if (typeof window === "undefined") return null
  const prefix = "/invite/"
  if (!window.location.pathname.startsWith(prefix)) return null
  const token = window.location.pathname.slice(prefix.length)
  return token.length > 0 ? token : null
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <Root />
      </AuthProvider>
    </QueryClientProvider>
  </StrictMode>,
)
