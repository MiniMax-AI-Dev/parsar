import { useEffect, useState } from "react"
import { useWorkspaceId } from "../../lib/workspace"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { StartSessionDialog } from "../../pages/admin/core/StartSessionDialog"

export function SessionLauncher() {
  const workspaceID = useWorkspaceId()
  const workspaces = useMyWorkspaces()
  const role = workspaces.data?.workspaces.find(workspace => workspace.id === workspaceID)?.role
  const [session, setSession] = useState<{ agentID?: string } | null>(null)
  useEffect(() => {
    const start = (event: Event) => setSession({ agentID: (event as CustomEvent<string | undefined>).detail })
    window.addEventListener("core:start-session", start)
    return () => window.removeEventListener("core:start-session", start)
  }, [])
  return session && workspaceID && role && role !== "viewer"
    ? <StartSessionDialog key={`${workspaceID}:${session.agentID ?? ''}`} workspaceID={workspaceID} initialAgentID={session.agentID} onClose={() => setSession(null)} />
    : null
}
