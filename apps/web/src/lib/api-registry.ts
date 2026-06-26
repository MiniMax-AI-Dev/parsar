import { useQuery } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import type {
  ListConnectorsResponse,
  ListGatewaysResponse,
} from "./api-types"

/* --- Query keys --------------------------------------------------------- */

const KEY_CONNECTORS = (pid: string) => ["admin", "connectors", pid] as const
const KEY_GATEWAYS = (wsId: string) => ["admin", "gateways", wsId] as const

/* --- Network ------------------------------------------------------------ */

async function listConnectors(pid: string | null): Promise<ListConnectorsResponse> {
  if (!pid) return { connectors: [] }
  return apiRequest<ListConnectorsResponse>(
    `/api/v1/projects/${encodeURIComponent(pid)}/connectors`
  )
}

async function listGateways(wsId: string | null): Promise<ListGatewaysResponse> {
  if (!wsId) return { gateways: [] }
  return apiRequest<ListGatewaysResponse>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/gateways`
  )
}

/* --- React Query hooks -------------------------------------------------- */

export function useProjectConnectors(pid: string | null) {
  return useQuery({
    queryKey: KEY_CONNECTORS(pid ?? "_none"),
    queryFn: () => listConnectors(pid),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useWorkspaceGateways(wsId: string | null) {
  return useQuery({
    queryKey: KEY_GATEWAYS(wsId ?? "_none"),
    queryFn: () => listGateways(wsId),
    retry: noUnreachableRetry,
    staleTime: 60_000,
  })
}
