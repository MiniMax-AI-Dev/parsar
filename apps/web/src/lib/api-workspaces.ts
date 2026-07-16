/**
 * Workspace picker data + CRUD for the admin header switcher.
 *
 * `useMyWorkspaces` falls back to a single "Demo Workspace" *placeholder*
 * when `/api/v1/me/workspaces` is unreachable so the header switcher still
 * renders something.
 */
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import type {
  CreateJoinRequestRequest,
  CreateJoinRequestResponse,
  CreateWorkspaceRequest,
  CreateWorkspaceResponse,
  ListDiscoverableWorkspacesResponse,
  ListMyWorkspacesResponse,
  ListPendingJoinRequestsResponse,
  PendingJoinRequest,
  UpdateWorkspaceRequest,
  UserWorkspace,
  WorkspaceMember,
} from "./api-types"

const KEY_MY_WORKSPACES = ["admin", "myWorkspaces"] as const
const KEY_DISCOVERABLE_WORKSPACES = ["admin", "discoverableWorkspaces"] as const
const KEY_DISCOVERABLE_WORKSPACES_PAGE = (
  q: string,
  limit: number,
  offset: number
) => ["admin", "discoverableWorkspaces", { q, limit, offset }] as const
const KEY_PENDING_JOIN_REQUESTS = (wsId: string) =>
  ["admin", "pendingJoinRequests", wsId] as const

const MOCK_WORKSPACES: UserWorkspace[] = [
  {
    id: "mock-ws-1",
    name: "Demo Workspace",
    slug: "demo",
    visibility: "private",
    role: "owner",
    created_at: new Date(Date.now() - 86_400_000 * 30).toISOString(),
    updated_at: new Date(Date.now() - 86_400_000 * 30).toISOString(),
  },
]

async function listMyWorkspacesRequest(): Promise<ListMyWorkspacesResponse> {
  return apiRequest<ListMyWorkspacesResponse>("/api/v1/me/workspaces", {
    method: "GET",
  })
}

export function useMyWorkspaces() {
  return useQuery({
    queryKey: KEY_MY_WORKSPACES,
    queryFn: async () => {
      try {
        return await listMyWorkspacesRequest()
      } catch {
        // Switcher must always render something — fall back to mock so the
        // popover doesn't show a permanent "empty" state when API is down.
        return {
          user_id: "mock-user",
          workspaces: MOCK_WORKSPACES,
        } satisfies ListMyWorkspacesResponse
      }
    },
    retry: noUnreachableRetry,
    staleTime: 60_000,
    // Membership can be granted from another account while this tab is open.
    // Refresh when the user returns so the switcher shows it immediately.
    refetchOnMount: "always",
    refetchOnWindowFocus: "always",
  })
}

/* --- Mutations ---------------------------------------------------- */

export function useCreateWorkspace() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateWorkspaceRequest) =>
      apiRequest<CreateWorkspaceResponse>("/api/v1/workspaces", {
        method: "POST",
        body,
      }),
    onSuccess: (data) => {
      // Make the new workspace selectable before the background refetch
      // completes; otherwise WorkspaceSwitcher can restore the old id.
      qc.setQueryData<ListMyWorkspacesResponse>(KEY_MY_WORKSPACES, (current) => {
        const workspaces = current?.workspaces ?? []
        if (workspaces.some((workspace) => workspace.id === data.workspace.id)) {
          return current
        }
        return {
          user_id: current?.user_id ?? data.member.user_id,
          workspaces: [...workspaces, data.workspace],
        }
      })
      return qc.invalidateQueries({ queryKey: KEY_MY_WORKSPACES })
    },
  })
}

export function useUpdateWorkspace() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ wsId, body }: { wsId: string; body: UpdateWorkspaceRequest }) =>
      apiRequest<UserWorkspace>(`/api/v1/workspaces/${wsId}`, {
        method: "PATCH",
        body,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: KEY_MY_WORKSPACES })
    },
  })
}

export function useArchiveWorkspace() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (wsId: string) =>
      apiRequest<UserWorkspace>(`/api/v1/workspaces/${wsId}/archive`, {
        method: "POST",
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: KEY_MY_WORKSPACES })
    },
  })
}

/* --- Self-service workspace join requests ------------------------- */

/**
 * Public workspaces the current user is allowed to request to join. Falls
 * back to an empty response when the backend is unreachable.
 *
 * Convention: switcher dropdown uses limit=5; DiscoverWorkspacesDialog uses
 * limit=20 + offset + q for paging/search.
 */
export interface UseDiscoverableWorkspacesArgs {
  q?: string
  limit?: number
  offset?: number
  /** Disable the query when the modal is closed. */
  enabled?: boolean
}

export function useDiscoverableWorkspaces(
  args: UseDiscoverableWorkspacesArgs = {}
) {
  const q = (args.q ?? "").trim()
  const limit = args.limit ?? 5
  const offset = args.offset ?? 0
  return useQuery({
    queryKey: KEY_DISCOVERABLE_WORKSPACES_PAGE(q, limit, offset),
    enabled: args.enabled ?? true,
    queryFn: async () => {
      const params = new URLSearchParams()
      if (q) params.set("q", q)
      params.set("limit", String(limit))
      params.set("offset", String(offset))
      try {
        return await apiRequest<ListDiscoverableWorkspacesResponse>(
          `/api/v1/me/discoverable-workspaces?${params.toString()}`,
          { method: "GET" }
        )
      } catch {
        return {
          user_id: "",
          workspaces: [],
          total: 0,
          limit,
          offset,
        } satisfies ListDiscoverableWorkspacesResponse
      }
    },
    retry: noUnreachableRetry,
    staleTime: 60_000,
  })
}

/** Submit a join request. On success, invalidate the discovery list and my-workspaces list. */
export function useRequestJoinWorkspace() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      wsId,
      body,
    }: {
      wsId: string
      body: CreateJoinRequestRequest
    }) =>
      apiRequest<CreateJoinRequestResponse>(
        `/api/v1/workspaces/${wsId}/join-requests`,
        { method: "POST", body }
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: KEY_DISCOVERABLE_WORKSPACES })
      qc.invalidateQueries({ queryKey: KEY_MY_WORKSPACES })
    },
  })
}

/** Requester self-withdraws their pending request. On 409 (already handled by
 *  the owner) the discovery list must also be invalidated so the UI catches up. */
export function useWithdrawJoinRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ wsId }: { wsId: string }) =>
      apiRequest<void>(
        `/api/v1/workspaces/${wsId}/join-requests/mine`,
        { method: "DELETE" }
      ),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: KEY_DISCOVERABLE_WORKSPACES })
      qc.invalidateQueries({ queryKey: KEY_MY_WORKSPACES })
    },
  })
}

/** owner/admin view: list of pending join requests for a workspace. */
export function usePendingJoinRequests(wsId: string | null) {
  return useQuery({
    queryKey: KEY_PENDING_JOIN_REQUESTS(wsId ?? "_none"),
    enabled: wsId !== null,
    queryFn: async () => {
      if (!wsId) {
        return {
          workspace_id: "",
          requests: [],
        } satisfies ListPendingJoinRequestsResponse
      }
      try {
        return await apiRequest<ListPendingJoinRequestsResponse>(
          `/api/v1/workspaces/${wsId}/join-requests`,
          { method: "GET" }
        )
      } catch {
        return {
          workspace_id: wsId,
          requests: [],
        } satisfies ListPendingJoinRequestsResponse
      }
    },
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

/** Approve/reject share the same mutation factory; only the action path differs. */
function useReviewJoinRequest(action: "approve" | "reject") {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      wsId,
      requestId,
    }: {
      wsId: string
      requestId: string
      request?: PendingJoinRequest
    }) =>
      apiRequest<WorkspaceMember>(
        `/api/v1/workspaces/${wsId}/join-requests/${requestId}/${action}`,
        { method: "POST" }
      ),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: KEY_PENDING_JOIN_REQUESTS(vars.wsId) })
    },
  })
}

export function useApproveJoinRequest() {
  return useReviewJoinRequest("approve")
}

export function useRejectJoinRequest() {
  return useReviewJoinRequest("reject")
}
