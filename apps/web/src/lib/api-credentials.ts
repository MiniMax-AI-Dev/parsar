import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"
import type {
  ListMyCredentialsResponse,
  UserCredential,
  UserCredentialCreateRequest,
  UserCredentialPatchRequest,
} from "./api-types"

export const KEY_MY_CREDENTIALS = ["profile", "myCredentials"] as const

async function listMyCredentials(): Promise<ListMyCredentialsResponse> {
  return apiRequest<ListMyCredentialsResponse>("/api/v1/me/credentials")
}

async function createMyCredential(body: UserCredentialCreateRequest): Promise<UserCredential> {
  return apiRequest<UserCredential>("/api/v1/me/credentials", { method: "POST", body })
}

async function patchMyCredential(id: string, body: UserCredentialPatchRequest): Promise<UserCredential> {
  return apiRequest<UserCredential>(`/api/v1/me/credentials/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body,
  })
}

async function deleteMyCredential(id: string): Promise<UserCredential> {
  return apiRequest<UserCredential>(`/api/v1/me/credentials/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
}

export function useMyCredentials() {
  return useQuery({
    queryKey: KEY_MY_CREDENTIALS,
    queryFn: listMyCredentials,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useCreateMyCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: createMyCredential,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MY_CREDENTIALS })
    },
  })
}

export function usePatchMyCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: UserCredentialPatchRequest }) =>
      patchMyCredential(id, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MY_CREDENTIALS })
    },
  })
}

export function useDeleteMyCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: deleteMyCredential,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MY_CREDENTIALS })
    },
  })
}
