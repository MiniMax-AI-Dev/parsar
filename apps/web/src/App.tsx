import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { AdminRouter } from "./pages/admin/AdminRouter"
import { LoginPage } from "./pages/LoginPage"
import { OnboardingPage } from "./pages/OnboardingPage"
import { JoinWorkspaceLanding } from "./pages/JoinWorkspaceLanding"
import { popPendingJoinIntent } from "./lib/join-intent"
import { InviteAcceptPage } from "./pages/InviteAcceptPage"
import { AuthProvider, useAuth } from "./lib/auth-context"
import { ThemeProvider } from "./lib/theme-provider"
import { useMyWorkspaces } from "./lib/api-workspaces"

function LoadingScreen({ message }: { message: string }) {
  return (
    <main className="grid min-h-screen place-items-center bg-surface text-base text-fg-subtle">
      {message}
    </main>
  )
}

function AuthedRoot() {
  const { t } = useTranslation("common")
  const wsQuery = useMyWorkspaces()

  if (wsQuery.isLoading) {
    return <LoadingScreen message={t("login.loading")} />
  }
  if ((wsQuery.data?.workspaces.length ?? 0) === 0) {
    return <OnboardingPage />
  }
  return <AdminRouter />
}

function Root() {
  const { t } = useTranslation("common")
  const { isLoading, isAuthenticated } = useAuth()
  const joinWsId = parseJoinWorkspaceId()
  const inviteToken = parseInviteToken()

  useEffect(() => {
    if (isLoading || !isAuthenticated) return
    if (joinWsId !== null) return
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

export function App() {
  return (
    <ThemeProvider>
      <AuthProvider>
        <Root />
      </AuthProvider>
    </ThemeProvider>
  )
}
