import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"

/* --- Wire types ---------------------------------------------------------
 *
 * Mirror server/internal/api/runtime/runtime.go::runtimeDTO.
 * runner_credential_hash is stripped server-side so it never appears here.
 */

export type RuntimeType = "sandbox" | "external" | "agent_daemon"
export type RuntimePlacement = "local_device" | "cloud_sandbox" | "external_agent"

/**
 * Heartbeat-derived runtime state. `pending_pairing` until the daemon
 * consumes the pair token; `offline`/`online` toggled by the heartbeat
 * sweeper; `error` reserved for fatal runner faults.
 */
export type RuntimeLiveness =
  | "pending_pairing"
  | "offline"
  | "online"
  | "error"

export type RuntimeAdminState = "enabled" | "disabled"

export interface Runtime {
  id: string
  workspace_id: string
  type: RuntimeType
  name: string
  /** Admin lifecycle state. Older responses may omit it. */
  admin_state?: RuntimeAdminState
  /** Heartbeat-derived runtime state. */
  liveness: RuntimeLiveness
  provider: "e2b_compatible" | "http_agent" | "agent_daemon" | "agent_daemon_sandbox"
  owner_user_id?: string | null
  hostname: string
  version: string
  last_heartbeat_at?: string | null
  pairing_token_expires_at?: string | null
  config: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface CreatePairingResponse {
  runtime: Runtime
  /** Plaintext one-shot token (prefix rtk_). Shown ONCE; never
   * persisted. */
  pairing_token: string
}

export interface AgentKindCapabilities {
  streaming?: boolean
  permissions?: boolean
  usage?: boolean
  resume?: boolean
}

export interface SupportedAgentKind {
  kind: string
  available: boolean
  version?: string
  capabilities?: AgentKindCapabilities
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function stringValue(v: unknown): string {
  return typeof v === "string" ? v.trim() : ""
}

function stringList(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((item): item is string => typeof item === "string" && item.trim() !== "").map((item) => item.trim()) : []
}

function capabilitySnapshotPresent(runtime: Runtime): boolean {
  return Array.isArray(runtime.config.supported_agent_kinds) || Array.isArray(runtime.config.supported_agent_kind_names)
}

function parseCapabilities(v: unknown): AgentKindCapabilities {
  if (!isRecord(v)) return {}
  return {
    streaming: v.streaming === true,
    permissions: v.permissions === true,
    usage: v.usage === true,
    resume: v.resume === true,
  }
}

export function supportedAgentKinds(runtime: Runtime): SupportedAgentKind[] {
  const rawKinds = runtime.config.supported_agent_kinds
  if (Array.isArray(rawKinds)) {
    return rawKinds.flatMap((item) => {
      if (!isRecord(item) || typeof item.kind !== "string" || item.kind.trim() === "") return []
      return [{
        kind: item.kind.trim(),
        available: item.available === true,
        version: typeof item.version === "string" && item.version.trim() !== "" ? item.version.trim() : undefined,
        capabilities: parseCapabilities(item.capabilities),
      } satisfies SupportedAgentKind]
    })
  }
  return stringList(runtime.config.supported_agent_kind_names).map((kind) => ({ kind, available: true }))
}

export function supportedAgentKindNames(runtime: Runtime): string[] {
  const explicit = stringList(runtime.config.supported_agent_kind_names)
  if (explicit.length > 0) return explicit
  return supportedAgentKinds(runtime)
    .filter((kind) => kind.available)
    .map((kind) => kind.kind)
}

export function runtimeSupportsAgentKind(runtime: Runtime, agentKind?: string): boolean {
  const kind = agentKind?.trim()
  if (!kind) return true
  if (!capabilitySnapshotPresent(runtime)) {
    return kind === "claude_code"
  }
  return supportedAgentKindNames(runtime).includes(kind)
}

export function isSandboxDaemonRuntime(runtime: Runtime): boolean {
  if (runtime.type !== "agent_daemon") return false
  const cfg = runtime.config
  return runtime.provider === "agent_daemon_sandbox" ||
    stringValue(cfg.created_by) === "sandbox_provider" ||
    stringValue(cfg.daemon_mode) === "sandbox" ||
    stringValue(cfg.sandbox_kind) !== "" ||
    stringValue(cfg["parsar.sandbox_kind"]) !== "" ||
    stringValue(cfg.sandbox_id) !== "" ||
    stringValue(cfg["parsar.sandbox_id"]) !== ""
}

export function isLocalDeviceRuntime(runtime: Runtime): boolean {
  return runtime.type === "agent_daemon" && !isSandboxDaemonRuntime(runtime)
}

export function isRuntimeSelectableForDispatch(runtime: Runtime): boolean {
  return runtime.liveness === "online"
}

/* --- Query keys --------------------------------------------------------- */

interface RuntimeListFilters {
  placement?: RuntimePlacement
  liveness?: RuntimeLiveness
  agentKind?: string
}

interface WorkspaceRuntimesOptions extends RuntimeListFilters {
  refetchInterval?: number | false
  refetchOnMount?: boolean | "always"
  staleTime?: number
}

const KEY_LIST = (workspaceID: string, type?: RuntimeType, filters?: RuntimeListFilters) =>
  [
    "admin",
    "runtimes",
    workspaceID,
    type ?? "all",
    filters?.placement ?? "",
    filters?.liveness ?? "",
    filters?.agentKind ?? "",
  ] as const

let cachedDevUserID: string | null | undefined

async function devAuthHeaders(): Promise<Record<string, string>> {
  if (cachedDevUserID === undefined) {
    try {
      const me = await apiRequest<{ user_id?: string }>("/api/v1/me")
      cachedDevUserID = me.user_id ?? null
    } catch {
      cachedDevUserID = null
    }
  }
  return cachedDevUserID ? { "X-Parsar-Dev-User-ID": cachedDevUserID } : {}
}

/* --- API surface -------------------------------------------------------- */

export async function listRuntimesRequest(workspaceID: string, type?: RuntimeType, filters?: RuntimeListFilters): Promise<Runtime[]> {
  const params = new URLSearchParams()
  if (type) params.set("type", type)
  if (filters?.placement) params.set("placement", filters.placement)
  if (filters?.liveness) params.set("liveness", filters.liveness)
  const agentKind = filters?.agentKind?.trim()
  if (agentKind) params.set("agent_kind", agentKind)
  const q = params.toString()
  const resp = await apiRequest<{ runtimes: Runtime[] }>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/runtimes${q ? `?${q}` : ""}`,
    { method: "GET", headers: await devAuthHeaders() },
  )
  return resp.runtimes ?? []
}

export async function createRuntimePairingRequest(
  workspaceID: string,
  name: string,
  opts?: { type?: RuntimeType; provider?: Runtime["provider"] },
): Promise<CreatePairingResponse> {
  const type = opts?.type ?? "agent_daemon"
  const provider = opts?.provider ?? "agent_daemon"
  return apiRequest<CreatePairingResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/runtimes`,
    {
      method: "POST",
      headers: await devAuthHeaders(),
      body: {
        type,
        provider,
        name,
      },
    },
  )
}

export async function deleteRuntimeRequest(workspaceID: string, runtimeID: string): Promise<void> {
  await apiRequest<null>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/runtimes/${encodeURIComponent(runtimeID)}`,
    { method: "DELETE", headers: await devAuthHeaders() },
  )
}

/* --- Hooks -------------------------------------------------------------- */

export function useWorkspaceRuntimes(
  workspaceID: string,
  type?: RuntimeType,
  options?: WorkspaceRuntimesOptions,
) {
  const filters: RuntimeListFilters = {
    placement: options?.placement,
    liveness: options?.liveness,
    agentKind: options?.agentKind,
  }
  return useQuery({
    queryKey: KEY_LIST(workspaceID, type, filters),
    queryFn: () => listRuntimesRequest(workspaceID, type, filters),
    enabled: Boolean(workspaceID),
    // Admin runtime pages want live online/offline freshness; pickers that
    // need a one-shot list opt out by passing refetchInterval: false to
    // avoid the timestamp on each row ticking while the user is reading.
    refetchInterval: options?.refetchInterval ?? 5_000,
    refetchOnMount: options?.refetchOnMount,
    staleTime: options?.staleTime,
    retry: noUnreachableRetry,
  })
}

export function useCreateRuntimePairing(workspaceID: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      name,
      type,
      provider,
    }: {
      name: string
      type?: RuntimeType
      provider?: Runtime["provider"]
    }) => createRuntimePairingRequest(workspaceID, name, { type, provider }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "runtimes", workspaceID] })
    },
  })
}

export function useDeleteRuntime(workspaceID: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (runtimeID: string) => deleteRuntimeRequest(workspaceID, runtimeID),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "runtimes", workspaceID] })
    },
  })
}
