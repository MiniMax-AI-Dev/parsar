import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"

/* --- Types (match server ScheduledTaskRead / ScheduledTaskRunRead) ------ */

export interface ScheduledTask {
  id: string
  project_agent_id: string
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

export interface ScheduledTaskRun {
  id: string
  conversation_id: string
  project_agent_id: string
  connector_type: string
  status: string
  failure_reason: string
  trigger_source: string
  trigger_channel: string
  trigger_ref_id: string
  created_at: string
  started_at: string | null
  finished_at: string | null
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

export interface ScheduledTasksByProjectResponse {
  scheduled_tasks: ScheduledTask[]
  total: number
  limit: number
  offset: number
}

export interface UseScheduledTasksByProjectOptions {
  offset?: number
  limit?: number
}

/* --- Query keys --------------------------------------------------------- */

// Page-scoped key carries offset/limit; mutations invalidate via the project
// prefix so every cached page refreshes (React Query matches keys by prefix).
const KEY_TASKS_BY_PROJECT_PREFIX = (projectID: string) =>
  ["admin", "scheduledTasksByProject", projectID] as const
const KEY_TASKS_BY_PROJECT = (projectID: string, offset: number, limit: number) =>
  [...KEY_TASKS_BY_PROJECT_PREFIX(projectID), offset, limit] as const
const KEY_TASK_RUNS = (taskID: string) =>
  ["admin", "scheduledTaskRuns", taskID] as const

/* --- Network ------------------------------------------------------------ */

async function listTasksByProject(
  projectID: string | null,
  offset: number,
  limit: number,
): Promise<ScheduledTasksByProjectResponse> {
  if (!projectID) return { scheduled_tasks: [], total: 0, limit, offset }
  return apiRequest<ScheduledTasksByProjectResponse>(
    `/api/v1/projects/${encodeURIComponent(projectID)}/scheduled-tasks`,
    { query: { limit, offset } },
  )
}

async function listTaskRuns(taskID: string | null): Promise<ScheduledTaskRun[]> {
  if (!taskID) return []
  return apiRequest<ScheduledTaskRun[]>(
    `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}/runs`,
  )
}

/* --- React Query hooks -------------------------------------------------- */

export function useScheduledTasksByProject(
  projectID: string | null,
  options: UseScheduledTasksByProjectOptions = {},
) {
  const { offset = 0, limit = 20 } = options
  return useQuery({
    queryKey: KEY_TASKS_BY_PROJECT(projectID ?? "_none", offset, limit),
    queryFn: () => listTasksByProject(projectID, offset, limit),
    enabled: Boolean(projectID),
    retry: noUnreachableRetry,
    staleTime: 15_000,
    // Keep the previous page on screen while the next one fetches.
    placeholderData: (prev) => prev,
  })
}

export function useScheduledTaskRuns(taskID: string | null) {
  return useQuery({
    queryKey: KEY_TASK_RUNS(taskID ?? "_none"),
    queryFn: () => listTaskRuns(taskID),
    enabled: Boolean(taskID),
    retry: noUnreachableRetry,
    staleTime: 10_000,
  })
}

// Listing is project-wide, but create targets one agent (picked in the dialog),
// so the create mutation carries projectAgentID while the rest key off taskID.
export function useCreateScheduledTask(projectID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ projectAgentID, body }: { projectAgentID: string; body: ScheduledTaskCreateRequest }) =>
      apiRequest<ScheduledTask>(
        `/api/v1/project-agents/${encodeURIComponent(projectAgentID)}/scheduled-tasks`,
        { method: "POST", body },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_PROJECT_PREFIX(projectID ?? "_none") })
    },
  })
}

export function useUpdateScheduledTask(projectID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ taskID, body }: { taskID: string; body: ScheduledTaskUpdateRequest }) =>
      apiRequest<ScheduledTask>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}`,
        { method: "PATCH", body },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_PROJECT_PREFIX(projectID ?? "_none") })
    },
  })
}

export function useDeleteScheduledTask(projectID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (taskID: string) =>
      apiRequest<void>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_PROJECT_PREFIX(projectID ?? "_none") })
    },
  })
}

export function useRunScheduledTaskNow(projectID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (taskID: string) =>
      apiRequest<{ run_id: string }>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}/run-now`,
        { method: "POST", body: {} },
      ),
    onSuccess: (_res, taskID) => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS_BY_PROJECT_PREFIX(projectID ?? "_none") })
      void qc.invalidateQueries({ queryKey: KEY_TASK_RUNS(taskID) })
    },
  })
}
