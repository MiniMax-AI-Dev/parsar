// Spec fragment API client. The import endpoint has two modes: preview
// (?confirm=false) returns parsed pieces without touching the DB; confirm
// persists. The hooks expose both so dialogs can preview before commit.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiRequest, noUnreachableRetry } from "./api-client"

export type SpecSource = "manual" | "agent" | "import" | "user" | "auto-review"

export interface SpecFragment {
  id: string
  workspace_id: string
  title: string
  body: string
  tags: string[]
  source: SpecSource
  created_by?: string
  agent_actor?: string
  created_at: string
  updated_at: string
}

export interface ListSpecFragmentsResponse {
  fragments: SpecFragment[]
}

export interface CreateSpecFragmentRequest {
  title: string
  body: string
  tags: string[]
}

export interface UpdateSpecFragmentRequest {
  title: string
  body: string
  tags: string[]
}

export interface ImportSpecPiece {
  title: string
  body: string
}

export interface ImportSpecPreviewResponse {
  pieces: ImportSpecPiece[]
  fragments: SpecFragment[]
}

export interface ImportSpecConfirmResponse {
  fragments: SpecFragment[]
  pieces: ImportSpecPiece[]
}

export const KEY_SPEC_FRAGMENTS = (workspaceID: string) =>
  ["admin", "specFragments", workspaceID] as const

function noWorkspaceError(): ApiError {
  return new ApiError({ status: 0, code: "no_workspace", message: "no workspace selected" })
}

async function listSpecFragments(workspaceID: string): Promise<ListSpecFragmentsResponse> {
  return apiRequest<ListSpecFragmentsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/spec/fragments`,
  )
}

async function createSpecFragment(
  workspaceID: string,
  body: CreateSpecFragmentRequest,
): Promise<SpecFragment> {
  return apiRequest<SpecFragment>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/spec/fragments`,
    { method: "POST", body },
  )
}

async function updateSpecFragment(
  workspaceID: string,
  fragmentID: string,
  body: UpdateSpecFragmentRequest,
): Promise<SpecFragment> {
  return apiRequest<SpecFragment>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/spec/fragments/${encodeURIComponent(fragmentID)}`,
    { method: "PATCH", body },
  )
}

async function deleteSpecFragment(workspaceID: string, fragmentID: string): Promise<void> {
  return apiRequest<void>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/spec/fragments/${encodeURIComponent(fragmentID)}`,
    { method: "DELETE" },
  )
}

async function previewSpecImport(
  workspaceID: string,
  text: string,
): Promise<ImportSpecPreviewResponse> {
  return apiRequest<ImportSpecPreviewResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/spec/import`,
    { method: "POST", body: { text, confirm: false } },
  )
}

async function confirmSpecImport(
  workspaceID: string,
  text: string,
): Promise<ImportSpecConfirmResponse> {
  return apiRequest<ImportSpecConfirmResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/spec/import`,
    { method: "POST", body: { text, confirm: true } },
  )
}

export function useSpecFragmentsQuery(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_SPEC_FRAGMENTS(workspaceID ?? "_none"),
    queryFn: () => listSpecFragments(workspaceID as string),
    enabled: !!workspaceID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useCreateSpecFragmentMutation(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateSpecFragmentRequest) => {
      if (!workspaceID) throw noWorkspaceError()
      return createSpecFragment(workspaceID, body)
    },
    onSuccess: () => {
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_SPEC_FRAGMENTS(workspaceID) })
      }
    },
  })
}

export function useUpdateSpecFragmentMutation(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ fragmentID, body }: { fragmentID: string; body: UpdateSpecFragmentRequest }) => {
      if (!workspaceID) throw noWorkspaceError()
      return updateSpecFragment(workspaceID, fragmentID, body)
    },
    onSuccess: () => {
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_SPEC_FRAGMENTS(workspaceID) })
      }
    },
  })
}

export function useDeleteSpecFragmentMutation(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (fragmentID: string) => {
      if (!workspaceID) throw noWorkspaceError()
      return deleteSpecFragment(workspaceID, fragmentID)
    },
    onSuccess: () => {
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_SPEC_FRAGMENTS(workspaceID) })
      }
    },
  })
}

export function usePreviewSpecImportMutation(workspaceID: string | null) {
  return useMutation({
    mutationFn: (text: string) => {
      if (!workspaceID) throw noWorkspaceError()
      return previewSpecImport(workspaceID, text)
    },
  })
}

export function useConfirmSpecImportMutation(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (text: string) => {
      if (!workspaceID) throw noWorkspaceError()
      return confirmSpecImport(workspaceID, text)
    },
    onSuccess: () => {
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_SPEC_FRAGMENTS(workspaceID) })
      }
    },
  })
}
