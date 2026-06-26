/**
 * Bootstrap: hydrate the workspace + project context from `/dev/seed`.
 * Skips when URL or localStorage already supplied an id. Silently falls
 * back to mock mode on any failure.
 */
import {
  isUUID,
  setWorkspaceId,
  setProjectId,
  getCurrentWorkspaceId,
  getCurrentProjectId,
} from "./workspace"

interface DevSeedResponse {
  db?: {
    workspace_id?: string
    project_id?: string
  }
}

let bootstrapped = false

export async function bootstrapWorkspace(): Promise<void> {
  if (bootstrapped) return
  bootstrapped = true

  const needWorkspace = !getCurrentWorkspaceId()
  const needProject = !getCurrentProjectId()
  if (!needWorkspace && !needProject) return

  try {
    const res = await fetch("/dev/seed", {
      headers: { Accept: "application/json" },
    })
    if (!res.ok) return
    const ct = res.headers.get("content-type") ?? ""
    if (!ct.includes("application/json")) return
    const body = (await res.json()) as DevSeedResponse
    if (needWorkspace && isUUID(body?.db?.workspace_id)) {
      setWorkspaceId(body.db!.workspace_id!)
    }
    if (needProject && isUUID(body?.db?.project_id)) {
      setProjectId(body.db!.project_id!)
    }
  } catch {
    // server unreachable — UI will fall through to mock mode.
  }
}
