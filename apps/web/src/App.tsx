import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { RefreshCw } from "lucide-react"
import { AdminRouter } from "./pages/admin/AdminRouter"
import { LoginPage } from "./pages/LoginPage"
import { OnboardingPage } from "./pages/OnboardingPage"
import { JoinWorkspaceLanding } from "./pages/JoinWorkspaceLanding"
import { popPendingJoinIntent, stashReturnTo } from "./lib/join-intent"
import { InviteAcceptPage } from "./pages/InviteAcceptPage"
import { SharedConversationPage } from "./pages/SharedConversationPage"
import { AuthProvider, useAuth } from "./lib/auth-context"
import { ThemeProvider } from "./lib/theme-provider"
import { ToastProvider } from "./components/ui/toast"
import { ErrorState } from "./components/ui/error-state"
import { Button } from "./components/ui/button"
import { useMyWorkspaces } from "./lib/api-workspaces"
import { SingleSlot } from "./components/plugin/SlotRenderer"
import { usePluginClients } from "./lib/use-plugins"
import { useWorkspaceId } from "./lib/workspace"

function LoadingScreen({ message }: { message: string }) {
  return (
    <main className="grid min-h-screen place-items-center bg-surface">
      <p className="text-sm text-fg-muted">{message}</p>
    </main>
  )
}

function AuthedRoot() {
  const { t } = useTranslation("common")
  const wsQuery = useMyWorkspaces()
  const retrying = wsQuery.fetchStatus !== "idle"
  const wsId = useWorkspaceId()
  usePluginClients(wsId)

  if (wsQuery.isPending) {
    return <LoadingScreen message={t("states.loading")} />
  }
  if (wsQuery.isError && !wsQuery.data?.workspaces.length) {
    return (
      <main className="grid min-h-screen place-items-center bg-surface p-6">
        <ErrorState
          title={t("workspaceSwitcher.loadFailed")}
          action={
            <Button size="sm" variant="outline" disabled={retrying} onClick={() => void wsQuery.refetch()}>
              <RefreshCw className={retrying ? "animate-spin" : undefined} strokeWidth={1.5} aria-hidden="true" />
              {retrying ? t("states.loading") : t("actions.retry")}
            </Button>
          }
        />
      </main>
    )
  }
  if (wsQuery.data?.workspaces.length === 0) {
    return <OnboardingPage />
  }
  // workspace.main slot: when a plugin registers here, it takes over
  // the entire page (full-screen). No navigation, no sidebar — the
  // plugin owns everything.
  return (
    <SingleSlot slotId="workspace.main" fallback={<AdminRouter />} />
  )
}

function Root() {
  const { t } = useTranslation("common")
  const { isLoading, isAuthenticated } = useAuth()
  const joinWsId = parseJoinWorkspaceId()
  const inviteToken = parseInviteToken()
  const sharedConversationId = parseSharedConversationId()

  useEffect(() => {
    if (isLoading || !isAuthenticated) return
    if (joinWsId !== null) return
    const destination = popPendingJoinIntent() ?? (window.location.pathname === "/login" ? "/" : null)
    if (destination) {
      window.location.replace(destination)
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
    // Preserve the destination before either password or SSO sign-in.
    stashReturnTo()
    return <LoginPage />
  }
  // A shared conversation needs a signed-in workspace member and nothing else,
  // so it renders above the console rather than inside it.
  if (sharedConversationId !== null) {
    return <SharedConversationPage conversationId={sharedConversationId} />
  }
  return <AuthedRoot />
}

function parseJoinWorkspaceId(): string | null {
  if (typeof window === "undefined") return null
  if (window.location.pathname !== "/join-workspace") return null
  const id = new URLSearchParams(window.location.search).get("id")
  return id && id.length > 0 ? id : null
}

function parseSharedConversationId(): string | null {
  if (typeof window === "undefined") return null
  const prefix = "/c/"
  if (!window.location.pathname.startsWith(prefix)) return null
  const id = window.location.pathname.slice(prefix.length).replace(/\/+$/, "")
  return id.length > 0 ? id : null
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
        <ToastProvider>
          <Root />
        </ToastProvider>
      </AuthProvider>
    </ThemeProvider>
  )
}
