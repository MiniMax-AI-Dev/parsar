import { useQuery } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import type { ListConnectorsResponse } from "./api-types"

/* --- Query keys --------------------------------------------------------- */

const KEY_CONNECTORS = (wsId: string) => ["admin", "connectors", wsId] as const

/* --- Network ------------------------------------------------------------ */

async function listConnectors(wsId: string | null): Promise<ListConnectorsResponse> {
  if (!wsId) return { connectors: [] }
  return apiRequest<ListConnectorsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/connector-usage`
  )
}

/* --- React Query hooks -------------------------------------------------- */

export function useWorkspaceConnectors(wsId: string | null) {
  return useQuery({
    queryKey: KEY_CONNECTORS(wsId ?? "_none"),
    queryFn: () => listConnectors(wsId),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}
