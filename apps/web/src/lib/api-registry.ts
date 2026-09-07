import { useQuery } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import type {
  ListGatewaysResponse,
} from "./api-types"

/* --- Query keys --------------------------------------------------------- */

const KEY_GATEWAYS = (wsId: string) => ["admin", "gateways", wsId] as const

/* --- Network ------------------------------------------------------------ */

async function listGateways(wsId: string | null): Promise<ListGatewaysResponse> {
  if (!wsId) return { gateways: [] }
  return apiRequest<ListGatewaysResponse>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/gateways`
  )
}

/* --- React Query hooks -------------------------------------------------- */

export function useWorkspaceGateways(wsId: string | null) {
  return useQuery({
    queryKey: KEY_GATEWAYS(wsId ?? "_none"),
    queryFn: () => listGateways(wsId),
    retry: noUnreachableRetry,
    staleTime: 60_000,
  })
}
