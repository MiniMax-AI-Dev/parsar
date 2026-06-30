/**
 * React Query hooks for the capability-import flow. Backend routes are
 * workspace-scoped; auth is workspace owner/admin (server enforces).
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiRequest, noUnreachableRetry } from "../../../lib/api-client"
import { KEY_CAPABILITIES_WORKSPACE, KEY_CAPABILITY_VERSIONS } from "../../../lib/api-capabilities"

import type {
  CreateCredentialKindRequest,
  CredentialKindRead,
  ImportCapabilityVersionCommitRequest,
  ImportCommitRequest,
  ImportCommitResponse,
  ImportPreviewRequest,
  ImportPreviewResponse,
  ListCredentialKindsResponse,
} from "./types"

/* ---------- query keys --------------------------------------------------- */

export const KEY_CREDENTIAL_KINDS = (workspaceID: string) =>
  ["admin", "credentialKinds", workspaceID] as const

/* ---------- uploads (plugin zip) ---------------------------------------- */

/**
 * Presigned upload response. The browser PUTs the bytes to uploadUrl
 * directly; the server never sees them. ossKey is a backend ref
 * (OSS key or pg:<uuid>), workspace-scoped so presign-download can
 * refuse cross-tenant reads. method + headers are backend-specific:
 * OSS returns PUT + a Content-Type pin; the PG proxy returns PUT with
 * the token baked into uploadUrl and no required headers. Both are
 * optional for back-compat with older servers (default PUT + octet).
 */
export interface PresignUploadResponse {
  uploadUrl: string
  ossKey: string
  method?: string
  headers?: Record<string, string>
  expiresAt: string
}

async function postPresignUpload(workspaceID: string, filename: string, prefix: string): Promise<PresignUploadResponse> {
  return apiRequest<PresignUploadResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/uploads/presign-upload`,
    {
      method: "POST",
      body: { filename, prefix },
    },
  )
}

export function usePresignUploadMutation(workspaceID: string | null) {
  return useMutation({
    mutationFn: ({ filename, prefix }: { filename: string; prefix: string }) => {
      if (!workspaceID) throw noWorkspaceError()
      return postPresignUpload(workspaceID, filename, prefix)
    },
    retry: noUnreachableRetry,
  })
}

/**
 * Direct upload to a presigned URL. Bypasses apiRequest because OSS
 * rejects extra signed headers (it signs Content-Type as a default
 * signed header, so the bytes must go up with exactly the headers the
 * server prescribed).
 *
 * method + headers come from the backend: OSS pins
 * Content-Type=application/octet-stream (matches oss.PresignPutContentType;
 * fetch's auto-detected "application/zip" would trigger
 * SignatureDoesNotMatch); the PG proxy authenticates via the token in the
 * URL and needs no signed headers. Falls back to PUT + octet-stream for
 * older servers that don't send method/headers.
 */
export async function putToPresignedURL(presign: PresignUploadResponse, file: File): Promise<void> {
  const resp = await fetch(presign.uploadUrl, {
    method: presign.method ?? "PUT",
    body: file,
    headers: presign.headers ?? { "Content-Type": "application/octet-stream" },
  })
  if (!resp.ok) {
    const detail = await resp.text().catch(() => "")
    throw new ApiError({
      status: resp.status,
      code: "oss_put_failed",
      message: `Upload failed: ${resp.status} ${resp.statusText}${detail ? ` — ${detail.slice(0, 200)}` : ""}`,
    })
  }
}

/**
 * @deprecated Use putToPresignedURL.
 */
export const uploadPluginZipDirect = putToPresignedURL

/* ---------- preview ------------------------------------------------------ */

async function postImportPreview(
  workspaceID: string,
  body: ImportPreviewRequest,
): Promise<ImportPreviewResponse> {
  return apiRequest<ImportPreviewResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/import/preview`,
    { method: "POST", body },
  )
}

/**
 * Manually-triggered (callers fire from a debounced effect) — paste
 * input is large and may be invalid; per-keystroke firing would thrash.
 */
export function useImportPreviewMutation(workspaceID: string | null) {
  return useMutation({
    mutationFn: (body: ImportPreviewRequest) => {
      if (!workspaceID) throw noWorkspaceError()
      return postImportPreview(workspaceID, body)
    },
    retry: noUnreachableRetry,
  })
}

/* ---------- commit ------------------------------------------------------- */

async function postImportCommit(
  workspaceID: string,
  body: ImportCommitRequest,
): Promise<ImportCommitResponse> {
  return apiRequest<ImportCommitResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/import/commit`,
    { method: "POST", body },
  )
}

export function useImportCommitMutation(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: ImportCommitRequest) => {
      if (!workspaceID) throw noWorkspaceError()
      return postImportCommit(workspaceID, body)
    },
    onSuccess: (res) => {
      // Same invalidations as useCreateCapability — the list view and the
      // version list of the newly-created capability both go stale.
      void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID ?? "_none") })
      void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_CAPABILITY_VERSIONS(workspaceID, res.capability.id) })
      }
    },
  })
}

/* ---------- version commit (add new version to existing capability) ----- */

async function postImportCapabilityVersionCommit(
  workspaceID: string,
  capabilityID: string,
  body: ImportCapabilityVersionCommitRequest,
): Promise<ImportCommitResponse> {
  return apiRequest<ImportCommitResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/capabilities/${encodeURIComponent(capabilityID)}/versions/import/commit`,
    { method: "POST", body },
  )
}

/**
 * Mutation for the "add new version" dialog. Mirrors useImportCommitMutation
 * but targets the version-only endpoint, so the response carries the existing
 * capability (no row creation) plus the new capability_version.
 *
 * Invalidates the version list for the affected capability + the capabilities
 * list (because Capability.latest_version updates).
 */
export function useImportCapabilityVersionMutation(
  workspaceID: string | null,
  capabilityID: string | null,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: ImportCapabilityVersionCommitRequest) => {
      if (!workspaceID) throw noWorkspaceError()
      if (!capabilityID) throw noCapabilityError()
      return postImportCapabilityVersionCommit(workspaceID, capabilityID, body)
    },
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID ?? "_none") })
      void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
      if (workspaceID) {
        void qc.invalidateQueries({ queryKey: KEY_CAPABILITY_VERSIONS(workspaceID, res.capability.id) })
      }
    },
  })
}

/* ---------- credential_kinds list --------------------------------------- */

async function getCredentialKinds(workspaceID: string): Promise<ListCredentialKindsResponse> {
  return apiRequest<ListCredentialKindsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/credential-kinds`,
  )
}

export function useCredentialKindsQuery(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_CREDENTIAL_KINDS(workspaceID ?? "_none"),
    queryFn: () => getCredentialKinds(workspaceID as string),
    enabled: !!workspaceID,
    retry: noUnreachableRetry,
    // Kinds change rarely; staying fresh for a few minutes keeps the
    // import picker snappy without holding stale rows after a sibling
    // inline-create.
    staleTime: 60_000,
  })
}

/* ---------- credential_kinds inline create ------------------------------ */

async function postCredentialKind(
  workspaceID: string,
  body: CreateCredentialKindRequest,
): Promise<CredentialKindRead> {
  return apiRequest<CredentialKindRead>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/credential-kinds`,
    { method: "POST", body },
  )
}

export function useCreateCredentialKindMutation(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateCredentialKindRequest) => {
      if (!workspaceID) throw noWorkspaceError()
      return postCredentialKind(workspaceID, body)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_CREDENTIAL_KINDS(workspaceID ?? "_none") })
    },
  })
}

/* ---------- helpers ----------------------------------------------------- */

function noWorkspaceError(): ApiError {
  return new ApiError({ status: 0, code: "no_workspace", message: "no workspace selected" })
}

function noCapabilityError(): ApiError {
  return new ApiError({ status: 0, code: "no_capability", message: "no capability selected" })
}
