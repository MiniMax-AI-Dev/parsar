import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"
import type {
  CreateModelRequest,
  CreateSecretRequest,
  ListModelsResponse,
  Model,
  UpdateModelRequest,
  Secret,
} from "./api-types"
import { randomHex } from "./random"

const KEY_MODELS = (workspaceID: string) =>
  ["admin", "models", workspaceID] as const
const KEY_SECRETS = (workspaceID: string) =>
  ["admin", "secrets", workspaceID] as const

/**
 * Model catalog is org-global; `workspaceID` only scopes the URL for RBAC.
 * `null` workspace maps to an empty list so the page can render before a
 * workspace is picked.
 */
async function listModels(workspaceID: string | null): Promise<ListModelsResponse> {
  if (!workspaceID) return { models: [] }
  return apiRequest<ListModelsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models`,
    { query: { limit: 200 } }
  )
}

async function disableModelRequest(workspaceID: string, modelID: string) {
  return apiRequest<Model>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models/${encodeURIComponent(modelID)}/disable`,
    { method: "POST" }
  )
}

async function deleteModelRequest(workspaceID: string, modelID: string): Promise<void> {
  await apiRequest<void>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models/${encodeURIComponent(modelID)}`,
    { method: "DELETE" }
  )
}

export interface BulkDeleteModelFailure {
  model_id: string
  error: string
  references?: { id: string; name: string }[]
}

export interface BulkDeleteModelsResponse {
  deleted: string[]
  failed: BulkDeleteModelFailure[]
}

async function bulkDeleteModelsRequest(
  workspaceID: string,
  modelIDs: string[],
): Promise<BulkDeleteModelsResponse> {
  return apiRequest<BulkDeleteModelsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models/bulk-delete`,
    { method: "POST", body: { model_ids: modelIDs } }
  )
}

async function updateModelRequest(
  workspaceID: string,
  modelID: string,
  body: UpdateModelRequest
): Promise<Model> {
  return apiRequest<Model>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models/${encodeURIComponent(modelID)}`,
    { method: "PATCH", body }
  )
}

async function createSecretRequest(
  workspaceID: string,
  body: CreateSecretRequest
): Promise<Secret> {
  return apiRequest<Secret>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/secrets`,
    { method: "POST", body }
  )
}

/* --- React Query hooks --------------------------------------------------- */

export function useModels(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_MODELS(workspaceID ?? "_none"),
    queryFn: () => listModels(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 30_000,
    // The model catalog can be changed from another admin session. Refresh
    // when the Agents/Models page mounts so the picker does not keep an old
    // cached inventory after navigation.
    refetchOnMount: "always",
  })
}

export function useDisableModel(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (modelID: string) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return disableModelRequest(workspaceID, modelID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
    },
  })
}

export function useDeleteModel(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (modelID: string) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return deleteModelRequest(workspaceID, modelID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
    },
  })
}

export function useBulkDeleteModels(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (modelIDs: string[]) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return bulkDeleteModelsRequest(workspaceID, modelIDs)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
    },
  })
}

export function useUpdateModel(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { modelID: string; body: UpdateModelRequest }) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return updateModelRequest(workspaceID, input.modelID, input.body)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
    },
  })
}

/* --- Inline create -------------------------------------------------------
 *
 * Creating a shared model is a single dialog submit even when no Secret
 * pre-exists. We chain:
 *   1. (optional) POST /secrets — when credential_mode === "inline_secret"
 *      AND the user pasted a raw key (instead of picking an existing Secret).
 *   2. POST /models — refs the secret_id from step 1, an existing secret_id,
 *      or a credential_kind_code (credential_ref mode).
 *
 * On chain failure between step 1 and step 2 we deliberately do NOT roll
 * back the Secret — the user can pick it from the Secret dropdown on retry.
 */
export interface InlineCreateModelInput {
  name: string
  provider_type: string
  adapter: string
  base_url: string
  model_key: string
  credential_mode: "inline_secret" | "credential_ref"
  /** inline_secret mode: paste a fresh key (we'll create the Secret). */
  api_key?: string
  /** inline_secret mode: reuse an existing Secret instead of api_key. */
  existing_secret_id?: string
  /** credential_ref mode: kind code (e.g. "anthropic_api_key"). */
  credential_kind_code?: string
  capabilities?: Record<string, boolean>
  limits?: Record<string, number>
  config?: Record<string, unknown>
}

function createModelBodyFromInput(
  input: InlineCreateModelInput,
  credential: { secretID?: string; credentialKindCode?: string },
): CreateModelRequest {
  return {
    name: input.name,
    provider_type: input.provider_type,
    adapter: input.adapter,
    base_url: input.base_url,
    model_key: input.model_key,
    credential_mode: input.credential_mode,
    secret_id: credential.secretID,
    credential_kind_code: credential.credentialKindCode,
    capabilities: input.capabilities,
    limits: input.limits,
    config: input.config,
  }
}

export function useCreateModel(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: InlineCreateModelInput): Promise<Model> => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }

      let secretID: string | undefined
      let credentialKindCode: string | undefined

      if (input.credential_mode === "inline_secret") {
        secretID = input.existing_secret_id
        if (!secretID) {
          if (!input.api_key || input.api_key.trim() === "") {
            throw new ApiError({
              status: 0,
              code: "api_key_required",
              message: "Either api_key or existing_secret_id is required for inline_secret mode",
              unreachable: false,
            })
          }
          const secret = await createSecretRequest(workspaceID, {
            name: `model-key-${randomHex(6)}`,
            kind: "model_provider",
            provider: input.provider_type,
            auth_type: "api_key",
            payload: { api_key: input.api_key.trim() },
          })
          secretID = secret.id
        }
      } else {
        // credential_ref mode
        credentialKindCode = input.credential_kind_code?.trim()
        if (!credentialKindCode) {
          throw new ApiError({
            status: 0,
            code: "credential_kind_required",
            message: "credential_kind_code is required for credential_ref mode",
            unreachable: false,
          })
        }
      }

      const body = createModelBodyFromInput(input, { secretID, credentialKindCode })

      return apiRequest<Model>(
        `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models`,
        { method: "POST", body }
      )
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
      void qc.invalidateQueries({ queryKey: KEY_SECRETS(workspaceID ?? "_none") })
    },
  })
}

export interface ImportProviderModelsInput {
  provider_type: string
  adapter: string
  base_url: string
  credential_mode: "inline_secret" | "credential_ref"
  api_key?: string
  secret_id?: string
  credential_kind_code?: string
  model_ids?: string[]
  dry_run?: boolean
  skip_existing?: boolean
  capabilities?: Record<string, unknown>
  limits?: Record<string, unknown>
  config?: Record<string, unknown>
}

export interface ImportProviderModelPreview {
  id: string
  exists: boolean
  supported_endpoint_types?: string[]
}

export interface ImportProviderModelFailure {
  model_key: string
  error: string
}

export interface ImportProviderModelsResponse {
  models: ImportProviderModelPreview[]
  created: Model[]
  skipped: string[]
  failed: ImportProviderModelFailure[]
}

export function useImportProviderModels(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: ImportProviderModelsInput): Promise<ImportProviderModelsResponse> => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return apiRequest<ImportProviderModelsResponse>(
        `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models/import`,
        { method: "POST", body: input },
      )
    },
    onSuccess: (_data, variables) => {
      if (!variables.dry_run) {
        void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
        void qc.invalidateQueries({ queryKey: KEY_SECRETS(workspaceID ?? "_none") })
      }
    },
  })
}

export interface DetectModelEndpointsInput {
  base_url: string
  model_key: string
  api_key?: string
  secret_id?: string
  credential_kind_code?: string
  config?: Record<string, unknown>
}

export interface DetectModelEndpointsResponse {
  supported_endpoint_types: string[]
}

async function detectModelEndpointsRequest(
  workspaceID: string,
  body: DetectModelEndpointsInput,
): Promise<DetectModelEndpointsResponse> {
  return apiRequest<DetectModelEndpointsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models/detect-endpoints`,
    { method: "POST", body },
  )
}

export function useDetectModelEndpoints(workspaceID: string | null) {
  return useMutation({
    mutationFn: async (input: DetectModelEndpointsInput): Promise<DetectModelEndpointsResponse> => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return detectModelEndpointsRequest(workspaceID, input)
    },
  })
}

/* --- Inline update -------------------------------------------------------
 *
 * credential_mode is locked at create time and cannot move between
 * inline_secret and credential_ref here.
 *
 * inline_secret mode: passing a fresh `api_key` chains a new Secret first
 * (the previous Secret stays around). Passing only `existing_secret_id` (==
 * current secret_id) leaves the binding untouched.
 */
export interface InlineUpdateModelInput {
  name: string
  model_key: string
  base_url?: string
  capabilities?: Record<string, boolean>
  limits?: Record<string, number>
  config?: Record<string, unknown>
  /** inline_secret mode: rotate to a brand-new key. */
  api_key?: string
  /** inline_secret mode: pick / keep an existing Secret. */
  existing_secret_id?: string
  /** Carried so we can name the auto-created Secret with this provider. */
  provider_type: string
  /** credential_ref mode: which kind to bind. */
  credential_kind_code?: string
  /** Which mode the model is in — drives which branches above are valid. */
  credential_mode: "inline_secret" | "credential_ref"
}

export function useUpdateModelInline(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      modelID: string
      values: InlineUpdateModelInput
    }): Promise<Model> => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }

      let secretID: string | undefined
      let credentialKindCode: string | undefined

      if (input.values.credential_mode === "inline_secret") {
        secretID = input.values.existing_secret_id
        if (input.values.api_key && input.values.api_key.trim() !== "") {
          const secret = await createSecretRequest(workspaceID, {
            name: `model-key-${randomHex(6)}`,
            kind: "model_provider",
            provider: input.values.provider_type,
            auth_type: "api_key",
            payload: { api_key: input.values.api_key.trim() },
          })
          secretID = secret.id
        }
      } else {
        credentialKindCode = input.values.credential_kind_code?.trim()
      }

      const body: UpdateModelRequest = {
        name: input.values.name,
        model_key: input.values.model_key,
        base_url: input.values.base_url,
        secret_id: secretID,
        credential_kind_code: credentialKindCode,
        capabilities: input.values.capabilities,
        limits: input.values.limits,
        config: input.values.config,
      }
      return updateModelRequest(workspaceID, input.modelID, body)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
      void qc.invalidateQueries({ queryKey: KEY_SECRETS(workspaceID ?? "_none") })
    },
  })
}

/* --- Connectivity test ---------------------------------------------------
 *
 * Sends a minimal chat-completion to verify base_url + key + headers +
 * model_key are wired correctly. credential_ref mode tests the caller's own
 * `user_credentials` row (by kind), not someone else's.
 */
export interface ModelConnectivityResult {
  supported: boolean
  success: boolean
  latency_ms: number
  http_status?: number
  endpoint_type?: string
  error?: string
  sample?: string
  healthy_count?: number
  total_count?: number
  results?: ModelConnectivityEndpointResult[]
}

export interface ModelConnectivityEndpointResult {
  endpoint_type: string
  supported: boolean
  success: boolean
  latency_ms: number
  http_status?: number
  failure_stage?: string
  error?: string
  sample?: string
  request?: ModelConnectivityHTTPRequest
  response?: ModelConnectivityHTTPResponse
}

export interface ModelConnectivityHTTPRequest {
  method: string
  url: string
  headers?: Record<string, string>
  body?: Record<string, unknown>
}

export interface ModelConnectivityHTTPResponse {
  status: number
  headers?: Record<string, string>
  body?: unknown
  raw_body?: string
  truncated?: boolean
}

function modelHealthFromConnectivityResult(result: ModelConnectivityResult): Record<string, unknown> {
  const status = result.success ? "healthy" : result.supported ? "failed" : "unsupported"
  const health: Record<string, unknown> = {
    status,
    checked_at: new Date().toISOString(),
    latency_ms: result.latency_ms,
    supported: result.supported,
    healthy_count: result.healthy_count,
    total_count: result.total_count,
  }
  if (result.http_status) health.http_status = result.http_status
  if (result.endpoint_type) health.endpoint_type = result.endpoint_type
  if (result.error) health.error = result.error
  if (result.sample) health.sample = result.sample
  if (result.results?.length) {
    health.results_summary = result.results.map((item) => {
      const summary: Record<string, unknown> = {
        endpoint_type: item.endpoint_type,
        supported: item.supported,
        success: item.success,
        latency_ms: item.latency_ms,
      }
      if (item.http_status) summary.http_status = item.http_status
      if (item.failure_stage) summary.failure_stage = item.failure_stage
      if (item.error) summary.error = item.error
      return summary
    })
  }
  return health
}

function updateCachedModelHealth(
  qc: ReturnType<typeof useQueryClient>,
  workspaceID: string,
  modelID: string,
  result: ModelConnectivityResult,
) {
  qc.setQueryData<ListModelsResponse>(KEY_MODELS(workspaceID), (current) => {
    if (!current) return current
    return {
      ...current,
      models: current.models.map((model) => {
        if (model.id !== modelID) return model
        return {
          ...model,
          config: {
            ...model.config,
            health: modelHealthFromConnectivityResult(result),
          },
        }
      }),
    }
  })
}

async function testModelRequest(
  workspaceID: string,
  modelID: string,
): Promise<ModelConnectivityResult> {
  return apiRequest<ModelConnectivityResult>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/models/${encodeURIComponent(modelID)}/test`,
    { method: "POST" },
  )
}

export function useTestModel(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (modelID: string) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      return testModelRequest(workspaceID, modelID)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
    },
  })
}

export interface BackgroundTestModelsInput {
  modelIDs: string[]
  onModelSettled?: (modelID: string) => void
}

export function useBackgroundTestModels(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: BackgroundTestModelsInput) => {
      if (!workspaceID) {
        throw new ApiError({
          status: 0,
          code: "no_workspace",
          message: "no workspace selected — pick a workspace first",
          unreachable: false,
        })
      }
      const ws = workspaceID
      const ids = Array.from(new Set(input.modelIDs.filter(Boolean)))
      const results: PromiseSettledResult<ModelConnectivityResult>[] = []
      let next = 0
      async function worker() {
        for (;;) {
          const index = next
          next += 1
          const modelID = ids[index]
          if (!modelID) return
          try {
            const result = await testModelRequest(ws, modelID)
            results[index] = { status: "fulfilled", value: result }
            updateCachedModelHealth(qc, ws, modelID, result)
          } catch (reason) {
            results[index] = { status: "rejected", reason }
          } finally {
            input.onModelSettled?.(modelID)
            void qc.invalidateQueries({ queryKey: KEY_MODELS(ws) })
          }
        }
      }
      await Promise.all(Array.from({ length: Math.min(3, ids.length) }, () => worker()))
      return results
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: KEY_MODELS(workspaceID ?? "_none") })
    },
  })
}
