import { useQuery } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import type {
  AuditSource,
  ListAuditRecordsResponse,
  ListUsageLogsResponse,
} from "./api-types"

/* --- Query keys --------------------------------------------------------- */

const KEY_AUDIT = (wsId: string, source: AuditSource | "all", targetType: string, targetID: string) =>
  ["admin", "auditRecords", wsId, source, targetType, targetID] as const
const KEY_USAGE = (wsId: string) => ["admin", "usage", wsId] as const

/* --- Network ------------------------------------------------------------ */

export interface AuditQuery {
  source?: AuditSource
  /** Exact match on `target_type` (e.g. agent_run, workspace). */
  target_type?: string
  /** Exact match on `target_id` — pin the feed to a specific resource. */
  target_id?: string
  /** Max rows; backend default 100, max 500. */
  limit?: number
}

async function listAuditRecords(
  wsId: string | null,
  q: AuditQuery
): Promise<ListAuditRecordsResponse> {
  if (!wsId) return { audit_records: [] }
  return apiRequest<ListAuditRecordsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/audit-records`,
    { query: { source: q.source, target_type: q.target_type, target_id: q.target_id, limit: q.limit ?? 200 } }
  )
}

async function listUsage(wsId: string | null): Promise<ListUsageLogsResponse> {
  if (!wsId) return { usage_logs: [] }
  return apiRequest<ListUsageLogsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/usage`,
    { query: { limit: 200 } }
  )
}


/* --- React Query hooks -------------------------------------------------- */

export function useAuditRecords(wsId: string | null, q: AuditQuery = {}) {
  return useQuery({
    queryKey: KEY_AUDIT(wsId ?? "_none", q.source ?? "all", q.target_type ?? "", q.target_id ?? ""),
    queryFn: () => listAuditRecords(wsId, q),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useUsage(wsId: string | null) {
  return useQuery({
    queryKey: KEY_USAGE(wsId ?? "_none"),
    queryFn: () => listUsage(wsId),
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}
