import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"
import type { ConnectivityResult } from "./api-runtime"

/* --- Wire types ---------------------------------------------------------
 *
 * Mirror server/internal/dev/sandbox_admin.go::sandboxStatusResponse.
 * status_kind collapses the granular killed_* variants into three colour
 * buckets so the panel doesn't need to enumerate every killed variant.
 */

export type SandboxStatusKind = "live" | "transient" | "terminal"

export interface SandboxBinding {
  binding_id: string
  workspace_id: string
  agent_id: string | null
  name?: string | null
  cache_key: string
  sandbox_id: string
  template_id: string
  status: string
  status_kind: SandboxStatusKind
  created_at: string
  last_active_at: string
  killed_at?: string
  /** e2b-side TTL — when e2b will reap the sandbox if no Renew arrives.
   * Omitted when the daemon manager couldn't reach e2b for a live read
   * (sibling-pod owner, transient API blip); UI renders absence as "unknown". */
  expires_at?: string
  metadata: Record<string, unknown>
}

interface SandboxRenewResponse {
  action: string
  binding_id: string
  sandbox_id: string
  /** Refreshed e2b endAt. Omitted when Renew succeeded but the GetInfo
   * follow-up failed; UI falls back to invalidating the panel query. */
  expires_at?: string
  message?: string
}

interface SandboxLifecycleResponse {
  action: string
  binding_id: string
  sandbox_id: string
}

/* --- Query keys --------------------------------------------------------- */

const KEY_SANDBOX = (workspaceID: string, agentID: string) =>
  ["admin", "sandbox", workspaceID, agentID] as const

/* --- Network ------------------------------------------------------------ */

async function getSandboxBinding(
  workspaceID: string,
  agentID: string,
): Promise<SandboxBinding | null> {
  // Server returns 200 + JSON `null` for "no binding". 404-catch below
  // covers older servers that still surface "no binding" as a 404.
  try {
    return await apiRequest<SandboxBinding | null>(
      `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/sandbox`,
    )
  } catch (err) {
    if (err instanceof ApiError && err.envelope.status === 404) return null
    throw err
  }
}

async function killSandboxRequest(
  workspaceID: string,
  agentID: string,
): Promise<SandboxLifecycleResponse> {
  return apiRequest<SandboxLifecycleResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/sandbox/kill`,
    { method: "POST" },
  )
}

/**
 * killSandboxRequestRaw exposes the kill HTTP call without the React-Query
 * mutation wrapper, for imperative callers (e.g. RuntimePage bulk-kill loop).
 * Non-2xx responses throw ApiError — caller must handle / collect those.
 */
export async function killSandboxRequestRaw(
  workspaceID: string,
  agentID: string,
): Promise<SandboxLifecycleResponse> {
  return killSandboxRequest(workspaceID, agentID)
}

async function rebuildSandboxRequest(
  workspaceID: string,
  agentID: string,
): Promise<SandboxLifecycleResponse> {
  return apiRequest<SandboxLifecycleResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/sandbox/rebuild`,
    { method: "POST" },
  )
}

/* --- Hooks -------------------------------------------------------------- */

/**
 * useSandboxBinding fetches the current sandbox binding for an agent.
 * Returns `null` (not error) when no live binding exists.
 */
export function useSandboxBinding(
  workspaceID: string | null,
  agentID: string | null,
) {
  return useQuery({
    queryKey: KEY_SANDBOX(workspaceID ?? "_none", agentID ?? "_none"),
    queryFn: () => {
      if (!workspaceID || !agentID) throw new Error("workspaceID + agentID required")
      return getSandboxBinding(workspaceID, agentID)
    },
    enabled: !!workspaceID && !!agentID,
    retry: noUnreachableRetry,
    // Short stale time so the panel reflects sandbox lifecycle without
    // spamming the server; refetchInterval keeps the open panel "live".
    staleTime: 10_000,
    refetchInterval: 15_000,
  })
}

export function useKillSandbox(workspaceID: string | null, agentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!workspaceID || !agentID) {
        throw new ApiError({
          status: 0,
          code: "no_target",
          message: "workspaceID + agentID required for sandbox kill",
          unreachable: false,
        })
      }
      return killSandboxRequest(workspaceID, agentID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "sandbox"] })
    },
  })
}

export function useRebuildSandbox(workspaceID: string | null, agentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!workspaceID || !agentID) {
        throw new ApiError({
          status: 0,
          code: "no_target",
          message: "workspaceID + agentID required for sandbox rebuild",
          unreachable: false,
        })
      }
      return rebuildSandboxRequest(workspaceID, agentID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "sandbox"] })
    },
  })
}

/* --- Renew (extend e2b TTL on a live sandbox) ------------------------- */

async function renewSandboxRequest(
  workspaceID: string,
  agentID: string,
): Promise<SandboxRenewResponse> {
  return apiRequest<SandboxRenewResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/sandbox/renew`,
    { method: "POST" },
  )
}

/**
 * useRenewSandbox extends the e2b-side TTL on the agent's live sandbox.
 * 409 means "this pod doesn't own the cache entry"; the panel's 15s poll
 * usually redirects to the owning pod within one cycle.
 */
export function useRenewSandbox(workspaceID: string | null, agentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!workspaceID || !agentID) {
        throw new ApiError({
          status: 0,
          code: "no_target",
          message: "workspaceID + agentID required for sandbox renew",
          unreachable: false,
        })
      }
      return renewSandboxRequest(workspaceID, agentID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "sandbox"] })
    },
  })
}

/* --- Acquire (manual provision) ---------------------------------------- */

async function acquireSandboxRequest(
  workspaceID: string,
  agentID: string,
): Promise<{ status: string; agent_id?: string }> {
  return apiRequest(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/sandbox/acquire`,
    { method: "POST" },
  )
}

/**
 * useAcquireSandbox triggers sandbox provisioning for an agent with
 * no active binding. Returns 202 immediately — the cold start runs in the
 * background and useSandboxBinding's 15s poll picks up the result.
 */
export function useAcquireSandbox(workspaceID: string | null, agentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!workspaceID || !agentID) {
        throw new ApiError({
          status: 0,
          code: "no_target",
          message: "workspaceID + agentID required for sandbox acquire",
          unreachable: false,
        })
      }
      return acquireSandboxRequest(workspaceID, agentID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "sandbox"] })
    },
  })
}

/* --- Workspace-scoped list (admin Sandboxes page) ----------------------- */

interface ListSandboxesResponse {
  sandboxes: SandboxBinding[]
}

const KEY_WORKSPACE_SANDBOXES = (workspaceID: string) =>
  ["admin", "sandboxes", workspaceID] as const

async function listWorkspaceSandboxes(workspaceID: string): Promise<SandboxBinding[]> {
  const res = await apiRequest<ListSandboxesResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/sandboxes`,
  )
  return res.sandboxes ?? []
}

/**
 * useWorkspaceSandboxes fetches every active sandbox binding in a workspace.
 * 503 (sandbox lifecycle not wired — server in local mode) surfaces as an
 * ApiError so the page can render a clear "switch to sandbox runtime" message.
 */
export function useWorkspaceSandboxes(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_WORKSPACE_SANDBOXES(workspaceID ?? "_none"),
    queryFn: () => {
      if (!workspaceID) throw new Error("workspaceID required")
      return listWorkspaceSandboxes(workspaceID)
    },
    enabled: !!workspaceID,
    retry: noUnreachableRetry,
    // last_active_at moves on every prompt — poll so admins don't see a
    // stale snapshot from when they opened the tab.
    staleTime: 10_000,
    refetchInterval: 15_000,
  })
}

/* --- Per-binding connectivity test ---------------------------------------- */

const SANDBOX_TEST_CONNECTION_TIMEOUT_MS = 30_000

async function testSandboxConnectionRequest(
  workspaceID: string,
  agentID: string,
): Promise<ConnectivityResult> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), SANDBOX_TEST_CONNECTION_TIMEOUT_MS)
  try {
    return await apiRequest<ConnectivityResult>(
      `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/agents/${encodeURIComponent(agentID)}/sandbox/test-connection`,
      { method: "POST", body: {}, signal: controller.signal },
    )
  } catch (err) {
    if (controller.signal.aborted) {
      throw new ApiError({
        status: 0,
        code: "sandbox_test_timeout",
        message: "sandbox connectivity test timed out",
        unreachable: false,
      })
    }
    throw err
  } finally {
    clearTimeout(timer)
  }
}

export function useSandboxConnectivityTest() {
  return useMutation({
    mutationFn: async (params: {
      workspaceID: string
      agentID: string
    }): Promise<ConnectivityResult> => {
      return testSandboxConnectionRequest(params.workspaceID, params.agentID)
    },
    retry: false,
  })
}
