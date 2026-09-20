import { useEffect } from "react"
import { useWorkspaceId } from "../../lib/workspace"
import { useAdminView } from "../../lib/admin-router"

export function SessionLauncher() {
  const workspaceID = useWorkspaceId()
  const { navigate } = useAdminView()
  useEffect(() => {
    const start = (event: Event) => {
      if (!workspaceID) return
      navigate("conversations", { focus: "compose", agent: (event as CustomEvent<string | undefined>).detail })
    }
    window.addEventListener("core:start-session", start)
    return () => window.removeEventListener("core:start-session", start)
  }, [workspaceID, navigate])
  return null
}
