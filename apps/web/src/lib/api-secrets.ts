import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"
import type {
  CreateSecretRequest,
  ListSecretsResponse,
  Secret,
} from "./api-types"

const KEY_SECRETS = (workspaceID: string) =>
  ["admin", "secrets", workspaceID] as const

/* --- Network --------------------------------------------------------------- */

async function listSecretsRequest(
  workspaceID: string | null
): Promise<ListSecretsResponse> {
  if (!workspaceID) return { secrets: [] }
  return apiRequest<ListSecretsResponse>(
    `/api/v1/workspaces/${workspaceID}/secrets`,
    { method: "GET" }
  )
}

async function createSecretRequest(
  workspaceID: string,
  body: CreateSecretRequest
): Promise<Secret> {
  return apiRequest<Secret>(
    `/api/v1/workspaces/${workspaceID}/secrets`,
    {
      method: "POST",
      body,
    }
  )
}

async function disableSecretRequest(
  workspaceID: string,
  secretID: string
): Promise<Secret> {
  return apiRequest<Secret>(
    `/api/v1/workspaces/${workspaceID}/secrets/${secretID}/disable`,
    { method: "POST" }
  )
}

/* --- Hooks ----------------------------------------------------------------- */

export function useSecrets(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_SECRETS(workspaceID ?? "_none"),
    queryFn: () => listSecretsRequest(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 15_000,
  })
}

export function useCreateSecret(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      body: CreateSecretRequest
    }) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return createSecretRequest(workspaceID, input.body)
    },
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: KEY_SECRETS(workspaceID ?? "_none"),
      })
    },
  })
}

export function useDisableSecret(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (secretID: string) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return disableSecretRequest(workspaceID, secretID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: KEY_SECRETS(workspaceID ?? "_none"),
      })
    },
  })
}
