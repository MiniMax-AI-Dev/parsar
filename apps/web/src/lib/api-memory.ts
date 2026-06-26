// Memory endpoint is NOT workspace-scoped at the URL: the server reads
// ?scope=user|project and routes internally. scope=user uses the session
// user; scope=project requires project_id for RBAC. User memory follows the
// account across workspaces, hence no workspace prefix.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"
import type { SpecSource } from "./api-specs"

export type MemoryScope = "user" | "project"
export type MemoryType = "user" | "feedback" | "project" | "reference"

export interface Memory {
  id: string
  scope: MemoryScope
  user_id: string
  project_id?: string
  memory_type: MemoryType
  title?: string
  body: string
  why?: string
  tags: string[]
  source: SpecSource
  agent_actor?: string
  conversation_id?: string
  created_at: string
  updated_at: string
}

export interface ListMemoriesResponse {
  memories: Memory[]
}

export interface CreateMemoryRequest {
  scope: MemoryScope
  /** required when scope=project */
  project_id?: string
  memory_type: MemoryType
  title?: string
  body: string
  why?: string
  tags: string[]
}

export interface UpdateMemoryRequest {
  title: string
  body: string
  why: string
  tags: string[]
}

// Memory-type / tag filters are intentionally not part of the key — we always
// fetch the full list and filter client-side.
export const KEY_USER_MEMORIES = ["admin", "memories", "user"] as const
export const KEY_PROJECT_MEMORIES = (projectID: string) =>
  ["admin", "memories", "project", projectID] as const

function missingProjectError(): ApiError {
  return new ApiError({ status: 0, code: "no_project", message: "no project selected" })
}

async function listUserMemories(): Promise<ListMemoriesResponse> {
  return apiRequest<ListMemoriesResponse>(`/api/v1/memories?scope=user`)
}

async function listProjectMemories(projectID: string): Promise<ListMemoriesResponse> {
  return apiRequest<ListMemoriesResponse>(
    `/api/v1/memories?scope=project&project_id=${encodeURIComponent(projectID)}`,
  )
}

async function createMemory(body: CreateMemoryRequest): Promise<Memory> {
  return apiRequest<Memory>(`/api/v1/memories`, { method: "POST", body })
}

async function updateMemory(memoryID: string, body: UpdateMemoryRequest): Promise<Memory> {
  return apiRequest<Memory>(`/api/v1/memories/${encodeURIComponent(memoryID)}`, {
    method: "PATCH",
    body,
  })
}

async function deleteMemory(memoryID: string): Promise<void> {
  return apiRequest<void>(`/api/v1/memories/${encodeURIComponent(memoryID)}`, {
    method: "DELETE",
  })
}

export function useUserMemoriesQuery() {
  return useQuery({
    queryKey: KEY_USER_MEMORIES,
    queryFn: () => listUserMemories(),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useProjectMemoriesQuery(projectID: string | null) {
  return useQuery({
    queryKey: KEY_PROJECT_MEMORIES(projectID ?? "_none"),
    queryFn: () => listProjectMemories(projectID as string),
    enabled: !!projectID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

// Pick the right cache key from the request body — callers don't need to
// pass a query-key hint.
export function useCreateMemoryMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateMemoryRequest) => {
      if (body.scope === "project" && !body.project_id) throw missingProjectError()
      return createMemory(body)
    },
    onSuccess: (_data, variables) => {
      if (variables.scope === "user") {
        void qc.invalidateQueries({ queryKey: KEY_USER_MEMORIES })
      } else if (variables.project_id) {
        void qc.invalidateQueries({ queryKey: KEY_PROJECT_MEMORIES(variables.project_id) })
      }
    },
  })
}

// Caller passes scope through so we pick the right cache key without
// re-fetching the row first.
export interface MutateMemoryArgs {
  memoryID: string
  scope: MemoryScope
  /** required when scope=project */
  projectID?: string
}

export function useUpdateMemoryMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      memoryID,
      body,
    }: MutateMemoryArgs & { body: UpdateMemoryRequest }) => updateMemory(memoryID, body),
    onSuccess: (_data, variables) => {
      if (variables.scope === "user") {
        void qc.invalidateQueries({ queryKey: KEY_USER_MEMORIES })
      } else if (variables.projectID) {
        void qc.invalidateQueries({ queryKey: KEY_PROJECT_MEMORIES(variables.projectID) })
      }
    },
  })
}

export function useDeleteMemoryMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ memoryID }: MutateMemoryArgs) => deleteMemory(memoryID),
    onSuccess: (_data, variables) => {
      if (variables.scope === "user") {
        void qc.invalidateQueries({ queryKey: KEY_USER_MEMORIES })
      } else if (variables.projectID) {
        void qc.invalidateQueries({ queryKey: KEY_PROJECT_MEMORIES(variables.projectID) })
      }
    },
  })
}
