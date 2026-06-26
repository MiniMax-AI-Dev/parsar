import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"
import type {
  AddWorkspaceMemberRequest,
  AddWorkspaceMemberResponse,
  ListWorkspaceMembersResponse,
  MemberRole,
  RemoveWorkspaceMemberResponse,
  WorkspaceMember,
} from "./api-types"

const KEY_WORKSPACE_MEMBERS = (wsId: string) =>
  ["admin", "workspaceMembers", wsId] as const

/* --- Network --------------------------------------------------------------- */

async function listWorkspaceMembersRequest(
  wsId: string | null
): Promise<ListWorkspaceMembersResponse> {
  if (!wsId) return { workspace_id: "", members: [] }
  return apiRequest<ListWorkspaceMembersResponse>(
    `/api/v1/workspaces/${wsId}/members`,
    { method: "GET" }
  )
}

/* --- Hooks ----------------------------------------------------------------- */

export function useWorkspaceMembers(wsId: string | null) {
  return useQuery({
    queryKey: KEY_WORKSPACE_MEMBERS(wsId ?? "_none"),
    queryFn: () => listWorkspaceMembersRequest(wsId),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

/* --- Write mutations ---------------------------------------------------- */

async function addWorkspaceMemberRequest(
  wsId: string,
  body: AddWorkspaceMemberRequest
): Promise<AddWorkspaceMemberResponse> {
  return apiRequest<AddWorkspaceMemberResponse>(
    `/api/v1/workspaces/${wsId}/members`,
    { method: "POST", body }
  )
}

async function updateWorkspaceMemberRoleRequest(
  wsId: string,
  userId: string,
  role: MemberRole
): Promise<WorkspaceMember> {
  return apiRequest<WorkspaceMember>(
    `/api/v1/workspaces/${wsId}/members/${userId}`,
    { method: "PATCH", body: { role } }
  )
}

async function removeWorkspaceMemberRequest(
  wsId: string,
  userId: string
): Promise<RemoveWorkspaceMemberResponse> {
  return apiRequest<RemoveWorkspaceMemberResponse>(
    `/api/v1/workspaces/${wsId}/members/${userId}`,
    { method: "DELETE" }
  )
}

export function useAddWorkspaceMember(wsId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: AddWorkspaceMemberRequest) => {
      if (!wsId) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return addWorkspaceMemberRequest(wsId, body)
    },
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: KEY_WORKSPACE_MEMBERS(wsId ?? "_none"),
      })
    },
  })
}

export function useUpdateWorkspaceMemberRole(wsId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { userId: string; role: MemberRole }) => {
      if (!wsId) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return updateWorkspaceMemberRoleRequest(wsId, input.userId, input.role)
    },
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: KEY_WORKSPACE_MEMBERS(wsId ?? "_none"),
      })
    },
  })
}

export function useRemoveWorkspaceMember(wsId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (userId: string) => {
      if (!wsId) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return removeWorkspaceMemberRequest(wsId, userId)
    },
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: KEY_WORKSPACE_MEMBERS(wsId ?? "_none"),
      })
    },
  })
}
