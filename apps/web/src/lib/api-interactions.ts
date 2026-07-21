import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"
import type {
  ListAgentInteractionsResponse,
  ResolveAgentInteractionRequest,
  ResolveAgentInteractionResponse,
} from "./api-types"

export type InteractionStatusGroup = "pending" | "decided" | "expired"

const key = (workspaceID: string, status: InteractionStatusGroup) =>
  ["admin", "interactions", workspaceID, status] as const

export function useAgentInteractions(workspaceID: string | null, status: InteractionStatusGroup) {
  return useQuery({
    queryKey: key(workspaceID ?? "_none", status),
    queryFn: () =>
      apiRequest<ListAgentInteractionsResponse>(
        `/api/v1/workspaces/${encodeURIComponent(workspaceID!)}/interactions`,
        { query: { status, limit: 200 } },
      ),
    enabled: !!workspaceID,
    retry: noUnreachableRetry,
    refetchInterval: status === "pending" ? 2_000 : 10_000,
  })
}

export function useResolveAgentInteraction(workspaceID: string | null) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: ResolveAgentInteractionRequest }) => {
      if (!workspaceID) throw new Error("workspace id is required")
      return apiRequest<ResolveAgentInteractionResponse>(
        `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/interactions/${encodeURIComponent(id)}/resolve`,
        { method: "POST", body },
      )
    },
    retry: false,
    onSuccess: () => {
      queryClient.invalidateQueries({
        predicate: (query) =>
          query.queryKey[0] === "admin" &&
          query.queryKey[1] === "interactions" &&
          query.queryKey[2] === workspaceID,
      })
    },
  })
}
