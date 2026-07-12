import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"

/* --- Types (match server ScheduledTaskRead / ScheduledTaskRunRead) ------ */

export interface ScheduledTask {
  id: string
  agent_id: string
  conversation_id: string
  name: string
  prompt: string
  cron_expr: string
  timezone: string
  enabled: boolean
  feishu_chat_id: string
  feishu_chat_name: string
  next_run_at: string | null
  last_run_at: string | null
  last_run_id: string
  last_status: string
  consecutive_failures: number
  created_by: string
  created_at: string
  updated_at: string
}

export interface ScheduledTaskCreateRequest {
  name: string
  prompt: string
  cron_expr: string
  timezone: string
  enabled?: boolean
  feishu_chat_id?: string
}

export interface ScheduledTaskUpdateRequest {
  name: string
  prompt: string
  cron_expr: string
  timezone: string
  enabled: boolean
}

export interface ScheduledTasksByWorkspaceResponse {
  scheduled_tasks: ScheduledTask[]
  total: number
  limit: number
  offset: number
}

export interface UseScheduledTasksByWorkspaceOptions {
  offset?: number
  limit?: number
}

/* --- Query keys --------------------------------------------------------- */

// Page-scoped key carries offset/limit; mutations invalidate via the workspace
// prefix so every cached page refreshes (React Query matches keys by prefix).
const KEY_TASKS_BY_WORKSPACE_PREFIX = (workspaceID: string) =>
  ["admin", "scheduledTasksByWorkspace", workspaceID] as const
const KEY_TASKS_BY_WORKSPACE = (workspaceID: string, offset: number, limit: number) =>
  [...KEY_TASKS_BY_WORKSPACE_PREFIX(workspaceID), offset, limit] as const
/* --- Network ------------------------------------------------------------ */

async function listTasksByWorkspace(
  workspaceID: string | null,
  offset: number,
  limit: number,
): Promise<ScheduledTasksByWorkspaceResponse> {
  if (!workspaceID) return { scheduled_tasks: [], total: 0, limit, offset }
  return apiRequest<ScheduledTasksByWorkspaceResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/scheduled-tasks`,
    { query: { limit, offset } },
  )
}

/* --- React Query hooks -------------------------------------------------- */

export function useScheduledTasksByWorkspace(
  workspaceID: string | null,
  options: UseScheduledTasksByWorkspaceOptions = {},
) {
  const { offset = 0, limit = 20 } = options
  return useQuery({
    queryKey: KEY_TASKS_BY_WORKSPACE(workspaceID ?? "_none", offset, limit),
    queryFn: () => listTasksByWorkspace(workspaceID, offset, limit),
    enabled: Boolean(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 15_000,
    // Keep the previous page on screen while the next one fetches.
    placeholderData: (prev) => prev,
  })
}

// Listing is workspace-wide, but create targets one agent (picked in the dialog),
// so the create mutation carries agentID while the rest key off taskID.
export function useCreateScheduledTask(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ agentID, body }: { agentID: string; body: ScheduledTaskCreateRequest }) =>
      apiRequest<ScheduledTask>(
        `/api/v1/agents/${encodeURIComponent(agentID)}/scheduled-tasks`,
        { method: "POST", body },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_WORKSPACE_PREFIX(workspaceID ?? "_none") })
    },
  })
}

export function useUpdateScheduledTask(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ taskID, body }: { taskID: string; body: ScheduledTaskUpdateRequest }) =>
      apiRequest<ScheduledTask>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}`,
        { method: "PATCH", body },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_WORKSPACE_PREFIX(workspaceID ?? "_none") })
    },
  })
}

export function useDeleteScheduledTask(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (taskID: string) =>
      apiRequest<void>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_WORKSPACE_PREFIX(workspaceID ?? "_none") })
    },
  })
}

export function useRunScheduledTaskNow(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (taskID: string) =>
      apiRequest<{ run_id: string }>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}/run-now`,
        { method: "POST", body: {} },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_WORKSPACE_PREFIX(workspaceID ?? "_none") })
    },
  })
}
