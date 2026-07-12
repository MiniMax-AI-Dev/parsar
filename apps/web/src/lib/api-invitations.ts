import { useMutation, useQuery } from "@tanstack/react-query"
import { apiRequest } from "./api-client"
import type { MemberRole } from "./api-types"

export interface CreateInvitationRequest {
  email: string
  name?: string
  role: MemberRole
}

export interface CreateInvitationResponse {
  invitation_id: string
  invite_link: string
  email: string
  role: MemberRole
  expires_at: string
}

export interface InviteInfoResponse {
  workspace_name: string
  email: string
  role: string
}

export interface AcceptInviteRequest {
  token: string
  password: string
}

export interface AcceptInviteResponse {
  user_id: string
  email: string
  workspace_id: string
}

export function useCreateInvitation(wsId: string | null) {
  return useMutation({
    mutationFn: async (body: CreateInvitationRequest) => {
      if (!wsId) throw new Error("no workspace selected")
      return apiRequest<CreateInvitationResponse>(
        `/api/v1/workspaces/${wsId}/invitations`,
        { method: "POST", body }
      )
    },
  })
}

export async function fetchInviteInfo(token: string): Promise<InviteInfoResponse> {
  return apiRequest<InviteInfoResponse>("/api/v1/invite/info", {
    method: "POST",
    body: { token },
  })
}

export function useInviteInfo(token: string) {
  return useQuery({
    queryKey: ["invite", "info", token],
    queryFn: () => fetchInviteInfo(token),
    enabled: !!token,
    retry: false,
  })
}

export async function acceptInviteRequest(body: AcceptInviteRequest): Promise<AcceptInviteResponse> {
  return apiRequest<AcceptInviteResponse>("/api/v1/invite/accept", {
    method: "POST",
    body,
  })
}

export function useAcceptInvite() {
  return useMutation({
    mutationFn: acceptInviteRequest,
  })
}
