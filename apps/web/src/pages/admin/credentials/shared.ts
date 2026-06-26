import { useMemo } from "react"
import { useQueries } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "../../../lib/api-client"
import { KEY_CAPABILITIES } from "../../../lib/api-capabilities"
import type {
  Capability,
  Model,
  UserCredential,
  UserWorkspace,
} from "../../../lib/api-types"

/** Where a missing-kind reference came from. Model refs are org-global
 *  (the model catalog is shared across workspaces). */
export type MissingCredentialRef =
  | {
      source: "capability"
      workspaceID: string
      workspaceName: string
      capabilityID: string
      capabilityName: string
    }
  | {
      source: "model"
      modelID: string
      modelName: string
    }

/** Aggregated "this credential kind is referenced by these things" record. */
export interface MissingCredentialRow {
  kind: string
  /** Distinct ref count. The same capability surfaced from two workspaces
   * counts twice on purpose. Model refs add to the same total. */
  refCount: number
  /** Expanded list shown when the card is opened. */
  refs: MissingCredentialRef[]
}

/**
 * Compute the set of credential kinds the user is missing.
 *
 * Scans two reference sources: active capabilities in the user's
 * workspaces (rows with `required: true`) and active org-global models
 * with `credential_mode === "credential_ref"`. Disabled rows are skipped.
 */
export function computeMissingCredentials(
  workspaces: UserWorkspace[],
  capabilitiesByWorkspace: Record<string, Capability[] | undefined>,
  models: Model[],
  myCredentials: UserCredential[],
): MissingCredentialRow[] {
  const haveKinds = new Set(myCredentials.map((c) => c.kind))
  const rowsByKind = new Map<string, MissingCredentialRow>()
  const ensureRow = (kind: string): MissingCredentialRow => {
    let row = rowsByKind.get(kind)
    if (!row) {
      row = { kind, refCount: 0, refs: [] }
      rowsByKind.set(kind, row)
    }
    return row
  }
  // (1) Capability-driven refs.
  for (const workspace of workspaces) {
    const caps = capabilitiesByWorkspace[workspace.id] ?? []
    for (const cap of caps) {
      if (cap.status !== "active") continue
      const reqs = cap.required_credentials ?? []
      for (const rc of reqs) {
        if (!rc.required) continue
        if (haveKinds.has(rc.kind)) continue
        const row = ensureRow(rc.kind)
        row.refCount += 1
        row.refs.push({
          source: "capability",
          workspaceID: workspace.id,
          workspaceName: workspace.name,
          capabilityID: cap.id,
          capabilityName: cap.name,
        })
      }
    }
  }
  // (2) Model-driven refs: credential_ref models require every caller
  //     to supply their own credential under the bound kind.
  for (const model of models) {
    if (model.status !== "active") continue
    if (model.credential_mode !== "credential_ref") continue
    const kind = model.credential_kind_code
    if (!kind) continue
    if (haveKinds.has(kind)) continue
    const row = ensureRow(kind)
    row.refCount += 1
    row.refs.push({
      source: "model",
      modelID: model.id,
      modelName: model.name,
    })
  }
  return Array.from(rowsByKind.values()).sort((a, b) => b.refCount - a.refCount)
}

/**
 * Drive the per-workspace capability fetch. Reuses KEY_CAPABILITIES so
 * the Capabilities page can share the prefetched cache entry.
 */
export function useCapabilitiesPerWorkspace(workspaces: UserWorkspace[]): {
  byWorkspace: Record<string, Capability[] | undefined>
  isLoading: boolean
  isError: boolean
} {
  const queries = useQueries({
    queries: workspaces.map((workspace) => ({
      queryKey: KEY_CAPABILITIES(workspace.id, "", ""),
      queryFn: () =>
        apiRequest<{ capabilities: Capability[] }>(
          `/api/v1/workspaces/${encodeURIComponent(workspace.id)}/capabilities`,
        ),
      enabled: workspaces.length > 0,
      retry: noUnreachableRetry,
      staleTime: 30_000,
    })),
  })

  return useMemo(() => {
    const byWorkspace: Record<string, Capability[] | undefined> = {}
    let isLoading = false
    let isError = false
    queries.forEach((query, idx) => {
      const workspace = workspaces[idx]
      if (!workspace) return
      if (query.isLoading) isLoading = true
      if (query.isError) isError = true
      byWorkspace[workspace.id] = query.data?.capabilities
    })
    return { byWorkspace, isLoading, isError }
    // Memo key tracks per-query state; workspaces identity is memoized by parent.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    queries.map((q) => `${q.dataUpdatedAt}:${q.isLoading}:${q.isError}`).join("|"),
    workspaces,
  ])
}

/**
 * Whether the current user can see + edit the Org Secrets tab.
 * Workspace owner/admin only; returns false when no workspace is bound.
 */
export function isWorkspaceAdmin(
  workspaceID: string | null,
  workspaces: UserWorkspace[] | undefined,
): boolean {
  if (!workspaceID) return false
  const ws = workspaces?.find((w) => w.id === workspaceID)
  if (!ws) return false
  return ws.role === "owner" || ws.role === "admin"
}
