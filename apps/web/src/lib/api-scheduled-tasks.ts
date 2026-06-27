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

/* --- Query keys --------------------------------------------------------- */

const KEY_TASKS = (projectAgentID: string) =>
  ["admin", "scheduledTasks", projectAgentID] as const
const KEY_TASK_RUNS = (taskID: string) =>
  ["admin", "scheduledTaskRuns", taskID] as const

/* --- Network ------------------------------------------------------------ */

async function listTasks(projectAgentID: string | null): Promise<ScheduledTask[]> {
  if (!projectAgentID) return []
  return apiRequest<ScheduledTask[]>(
    `/api/v1/project-agents/${encodeURIComponent(projectAgentID)}/scheduled-tasks`,
  )
}

async function listTaskRuns(taskID: string | null): Promise<ScheduledTaskRun[]> {
  if (!taskID) return []
  return apiRequest<ScheduledTaskRun[]>(
    `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}/runs`,
  )
}

/* --- React Query hooks -------------------------------------------------- */

export function useScheduledTasks(projectAgentID: string | null) {
  return useQuery({
    queryKey: KEY_TASKS(projectAgentID ?? "_none"),
    queryFn: () => listTasks(projectAgentID),
    enabled: Boolean(projectAgentID),
    retry: noUnreachableRetry,
    staleTime: 15_000,
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

export function useCreateScheduledTask(projectAgentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: ScheduledTaskCreateRequest) => {
      if (!projectAgentID) throw new Error("projectAgentID is required")
      return apiRequest<ScheduledTask>(
        `/api/v1/project-agents/${encodeURIComponent(projectAgentID)}/scheduled-tasks`,
        { method: "POST", body },
      )
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS(projectAgentID ?? "_none") })
    },
  })
}

export function useUpdateScheduledTask(projectAgentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ taskID, body }: { taskID: string; body: ScheduledTaskUpdateRequest }) =>
      apiRequest<ScheduledTask>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}`,
        { method: "PATCH", body },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS(projectAgentID ?? "_none") })
    },
  })
}

export function useDeleteScheduledTask(projectAgentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (taskID: string) =>
      apiRequest<void>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS(projectAgentID ?? "_none") })
    },
  })
}

export function useRunScheduledTaskNow(projectAgentID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (taskID: string) =>
      apiRequest<{ run_id: string }>(
        `/api/v1/scheduled-tasks/${encodeURIComponent(taskID)}/run-now`,
        { method: "POST", body: {} },
      ),
    onSuccess: (_res, taskID) => {
      void qc.invalidateQueries({ queryKey: KEY_TASKS(projectAgentID ?? "_none") })
      void qc.invalidateQueries({ queryKey: KEY_TASK_RUNS(taskID) })
    },
  })
}
