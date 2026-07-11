import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"

/* --- Wire types ---------------------------------------------------------
 *
 * Mirror of `GET /workspaces/{wid}/runtime/status`. Runtime mode is per-Agent
 * (`agents.config.runtime`), not per-workspace.
 *
 *   - has_credential:      workspace has a registered E2B credential
 *   - credential_masked:   safe redacted preview ("e2b_•••wxyz") or null
 *   - available:           true when the active sandbox provider is reachable.
 *                          oss/selfhost require a workspace credential first;
 *                          managed deployments are platform-configured.
 *   - sandbox_agent_count: number of agents in the workspace whose runtime
 *                          is "sandbox". Powers banner copy + empty-state
 *                          matrix branch.
 *   - profile:             deployment profile ("oss" / "managed" /
 *                          "selfhost")
 *   - configured_by:       legacy field; "ops" when server has
 *                          PARSAR_OPENCODE_RUNNER set. Informational only
 *                          (env no longer drives runtime selection).
 */

export type RuntimeMode = "sandbox" | "local"
export type RuntimeProfile = "oss" | "managed" | "selfhost"
export type RuntimeConfiguredBy = "ops" | "self"

export interface RuntimeStatus {
  has_credential: boolean
  credential_masked: string | null
  available: boolean
  sandbox_agent_count: number
  profile: RuntimeProfile
  configured_by?: RuntimeConfiguredBy
  sandbox_image?: string
}

const KEY_RUNTIME_STATUS = (workspaceID: string) =>
  ["admin", "runtime", "status", workspaceID] as const

async function getRuntimeStatus(workspaceID: string): Promise<RuntimeStatus> {
  return apiRequest<RuntimeStatus>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/runtime/status`,
  )
}

/**
 * useRuntimeStatus reads the workspace-scoped runtime status banner data.
 */
export function useRuntimeStatus(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_RUNTIME_STATUS(workspaceID ?? "_none"),
    queryFn: () => {
      if (!workspaceID) throw new Error("workspaceID required")
      return getRuntimeStatus(workspaceID)
    },
    enabled: !!workspaceID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })
}

/* --- Per-binding connectivity test types --------------------------------
 *
 * Used by the detail-page "🔬 Connectivity test" button. Probes an EXISTING
 * agent_daemon sandbox (paired + heartbeat freshness).
 *
 * `category` enums: credInvalid | quotaExceeded | unreachable | runtimeDown
 *                   | promptTimeout | unknown
 */

export type ConnectivityCheckCategory =
  | "credInvalid"
  | "quotaExceeded"
  | "unreachable"
  | "runtimeDown"
  | "promptTimeout"
  | "unknown"

export interface ConnectivityCheckError {
  /** Stable enum the panel maps to user-facing copy (see above). */
  category: ConnectivityCheckCategory
  /** Raw error string (kept for forensics tooltip, NOT primary copy). */
  detail?: string
}

export interface ConnectivityCheck {
  /**
   * Backend check identifier (e.g. "sandbox_connect" | "runtime_ready" |
   * "prompt_roundtrip"). Treated as opaque here — the panel maps to the
   * right i18n key via its `checkLabelFor` resolver prop.
   */
  name: string
  pass: boolean
  duration_ms: number
  /** null when the check passed; populated when it failed. */
  error: ConnectivityCheckError | null
}

export type ConnectivityOverall = "pass" | "partial" | "fail"

export interface ConnectivityResult {
  overall: ConnectivityOverall
  started_at: string
  duration_ms: number
  /** Set when the backend has a sandbox id to report on (paired). */
  sandbox_id?: string
  checks: ConnectivityCheck[]
}

/* --- Workspace runtime credential CRUD ---------------------------------- */

export interface RuntimeCredentialResponse {
  has_credential: boolean
  credential_masked: string | null
  updated_at: string
}

async function putRuntimeCredentialRequest(
  workspaceID: string,
  apiKey: string,
): Promise<RuntimeCredentialResponse> {
  return apiRequest<RuntimeCredentialResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/runtime/credential`,
    {
      method: "PUT",
      body: { api_key: apiKey },
    },
  )
}

async function deleteRuntimeCredentialRequest(
  workspaceID: string,
): Promise<RuntimeCredentialResponse> {
  return apiRequest<RuntimeCredentialResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/runtime/credential`,
    { method: "DELETE" },
  )
}

/** Register or overwrite the workspace sandbox provider API key. */
export function useSaveRuntimeCredential(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { apiKey: string }) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected",
          unreachable: false,
        })
      }
      return putRuntimeCredentialRequest(workspaceID, input.apiKey)
    },
    retry: false,
    onSuccess: () => {
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_RUNTIME_STATUS(workspaceID) })
      }
    },
  })
}

/**
 * useClearRuntimeCredential — null the workspace's runtime credential
 * pointer. Idempotent on the server side.
 */
export function useClearRuntimeCredential(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected",
          unreachable: false,
        })
      }
      return deleteRuntimeCredentialRequest(workspaceID)
    },
    retry: false,
    onSuccess: () => {
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_RUNTIME_STATUS(workspaceID) })
      }
    },
  })
}
