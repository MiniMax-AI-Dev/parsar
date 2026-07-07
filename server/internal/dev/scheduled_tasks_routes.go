package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/runtime/scheduler"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func scheduledTaskScope(w http.ResponseWriter, ctx context.Context, rs RuntimeStore, taskID string) (store.ScheduledTaskScope, bool) {
	scope, err := rs.GetScheduledTaskScope(ctx, taskID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownScheduledTask) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "scheduled task not found"})
			return store.ScheduledTaskScope{}, false
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve scheduled task"})
		return store.ScheduledTaskScope{}, false
	}
	return scope, true
}

// listScheduledTasks returns every scheduled task defined for one agent.
//
//	@Summary		List an agent's scheduled tasks
//	@Description	Returns all scheduled tasks defined on the given agent. Caller must be a workspace member.
//	@Tags			scheduled-tasks
//	@ID				listDevAgentScheduledTasks
//	@Produce		json
//	@Param			agentID	path		string					true	"Agent UUID"
//	@Success		200		{array}		map[string]interface{}	"Scheduled tasks for the agent"
//	@Failure		400		{object}	map[string]string		"agent_id must be a valid uuid"
//	@Failure		403		{object}	map[string]string		"Caller is not a workspace member"
//	@Failure		503		{object}	map[string]string		"Database-backed APIs are disabled"
//	@Router			/api/v1/agents/{agentID}/scheduled-tasks [get]
func listScheduledTasks(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		workspaceID, ok := workspaceIDForAgent(w, r.Context(), rs, agentID)
		if !ok {
			return
		}
		if err := requireWorkspaceMember(r, rs, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		tasks, err := rs.ListScheduledTasksByAgent(r.Context(), agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list scheduled tasks"})
			return
		}
		writeJSON(w, http.StatusOK, tasks)
	}
}

// listScheduledTasksByWorkspace returns every scheduled task in the workspace.
//
//	@Summary		List scheduled tasks for a workspace
//	@Description	Paginated list of every scheduled task in the workspace across all agents. Caller must be a workspace member.
//	@Tags			scheduled-tasks
//	@ID				listDevWorkspaceScheduledTasks
//	@Produce		json
//	@Param			workspaceID	path		string					true	"Workspace UUID"
//	@Param			limit		query		int						false	"Max rows to return (default 50)"
//	@Param			offset		query		int						false	"Row offset for pagination"
//	@Success		200			{object}	map[string]interface{}	"Paged scheduled task list"
//	@Failure		400			{object}	map[string]string		"workspace_id must be a valid uuid"
//	@Failure		403			{object}	map[string]string		"Caller is not a workspace member"
//	@Failure		503			{object}	map[string]string		"Database-backed APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/scheduled-tasks [get]
func listScheduledTasksByWorkspace(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, rs, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		limit := parseLimit(r, 50)
		offset := parseOffset(r)
		result, err := rs.ListScheduledTasksByWorkspace(r.Context(), workspaceID, limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list scheduled tasks"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"scheduled_tasks": result.Tasks,
			"total":           result.Total,
			"limit":           limit,
			"offset":          offset,
		})
	}
}

// getScheduledTask returns a single scheduled task by id.
//
//	@Summary		Get a scheduled task
//	@Description	Returns the scheduled task detail. Caller must be a member of the workspace owning the task.
//	@Tags			scheduled-tasks
//	@ID				getDevScheduledTask
//	@Produce		json
//	@Param			taskID	path		string					true	"Scheduled task UUID"
//	@Success		200		{object}	map[string]interface{}	"Scheduled task"
//	@Failure		400		{object}	map[string]string		"task_id must be a valid uuid"
//	@Failure		403		{object}	map[string]string		"Caller is not a workspace member"
//	@Failure		404		{object}	map[string]string		"Scheduled task not found"
//	@Failure		503		{object}	map[string]string		"Database-backed APIs are disabled"
//	@Router			/api/v1/scheduled-tasks/{taskID} [get]
func getScheduledTask(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
		if !isUUID(taskID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id must be a valid uuid"})
			return
		}
		scope, ok := scheduledTaskScope(w, r.Context(), rs, taskID)
		if !ok {
			return
		}
		if err := requireWorkspaceMember(r, rs, scope.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		task, err := rs.GetScheduledTask(r.Context(), taskID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownScheduledTask) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "scheduled task not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get scheduled task"})
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

// listScheduledTaskRuns returns the run history for one scheduled task.
//
//	@Summary		List runs for a scheduled task
//	@Description	Returns up to 50 most recent agent_run rows spawned by this scheduled task. Caller must be a workspace member.
//	@Tags			scheduled-tasks
//	@ID				listDevScheduledTaskRuns
//	@Produce		json
//	@Param			taskID	path		string					true	"Scheduled task UUID"
//	@Success		200		{array}		map[string]interface{}	"Recent runs for the scheduled task"
//	@Failure		400		{object}	map[string]string		"task_id must be a valid uuid"
//	@Failure		403		{object}	map[string]string		"Caller is not a workspace member"
//	@Failure		404		{object}	map[string]string		"Scheduled task not found"
//	@Failure		503		{object}	map[string]string		"Database-backed APIs are disabled"
//	@Router			/api/v1/scheduled-tasks/{taskID}/runs [get]
func listScheduledTaskRuns(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
		if !isUUID(taskID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id must be a valid uuid"})
			return
		}
		scope, ok := scheduledTaskScope(w, r.Context(), rs, taskID)
		if !ok {
			return
		}
		if err := requireWorkspaceMember(r, rs, scope.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		runs, err := rs.ListAgentRunsByScheduledTask(r.Context(), taskID, 50)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list runs"})
			return
		}
		writeJSON(w, http.StatusOK, runs)
	}
}

type scheduledTaskCreateBody struct {
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	CronExpr     string `json:"cron_expr"`
	Timezone     string `json:"timezone"`
	Enabled      *bool  `json:"enabled"`
	FeishuChatID string `json:"feishu_chat_id"`
}

// createScheduledTask creates a scheduled task for one agent.
//
//	@Summary		Create a scheduled task
//	@Description	Registers a new scheduled task on the given agent. cron_expr + timezone are validated and next_run_at is precomputed. Caller must be a workspace member with edit rights (not viewer).
//	@Tags			scheduled-tasks
//	@ID				createDevAgentScheduledTask
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path		string					true	"Agent UUID"
//	@Param			body	body		scheduledTaskCreateBody	true	"Scheduled task payload"
//	@Success		201		{object}	map[string]interface{}	"Created scheduled task"
//	@Failure		400		{object}	map[string]string		"agent_id invalid, body invalid, or cron_expr/timezone bad"
//	@Failure		403		{object}	map[string]string		"Caller is a viewer or not a workspace member"
//	@Failure		503		{object}	map[string]string		"Database-backed APIs are disabled"
//	@Router			/api/v1/agents/{agentID}/scheduled-tasks [post]
func createScheduledTask(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		workspaceID, ok := workspaceIDForAgent(w, r.Context(), rs, agentID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, rs, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body scheduledTaskCreateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		next, err := scheduler.NextRun(body.CronExpr, body.Timezone, time.Now().UTC())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		task, err := rs.CreateScheduledTask(r.Context(), store.CreateScheduledTaskInput{
			AgentID:      agentID,
			Name:         body.Name,
			Prompt:       body.Prompt,
			CronExpr:     body.CronExpr,
			Timezone:     body.Timezone,
			Enabled:      enabled,
			FeishuChatID: body.FeishuChatID,
			CreatedBy:    actorID,
			NextRunAt:    next,
		})
		if err != nil {
			if errors.Is(err, store.ErrInvalidInput) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, prompt, cron_expr, timezone are required"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create scheduled task"})
			return
		}
		writeJSON(w, http.StatusCreated, task)
	}
}

type scheduledTaskUpdateBody struct {
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	CronExpr string `json:"cron_expr"`
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}

// updateScheduledTask patches a scheduled task's schedule and prompt.
//
//	@Summary		Update a scheduled task
//	@Description	Overwrites the scheduled task's name/prompt/cron/timezone/enabled fields; next_run_at is recomputed from cron_expr + timezone. Caller must be a workspace member with edit rights.
//	@Tags			scheduled-tasks
//	@ID				updateDevScheduledTask
//	@Accept			json
//	@Produce		json
//	@Param			taskID	path		string					true	"Scheduled task UUID"
//	@Param			body	body		scheduledTaskUpdateBody	true	"Scheduled task update payload"
//	@Success		200		{object}	map[string]interface{}	"Updated scheduled task"
//	@Failure		400		{object}	map[string]string		"task_id invalid, body invalid, or cron_expr/timezone bad"
//	@Failure		403		{object}	map[string]string		"Caller is a viewer or not a workspace member"
//	@Failure		404		{object}	map[string]string		"Scheduled task not found"
//	@Failure		503		{object}	map[string]string		"Database-backed APIs are disabled"
//	@Router			/api/v1/scheduled-tasks/{taskID} [patch]
func updateScheduledTask(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
		if !isUUID(taskID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id must be a valid uuid"})
			return
		}
		scope, ok := scheduledTaskScope(w, r.Context(), rs, taskID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, rs, scope.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var body scheduledTaskUpdateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		next, err := scheduler.NextRun(body.CronExpr, body.Timezone, time.Now().UTC())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		task, err := rs.UpdateScheduledTask(r.Context(), store.UpdateScheduledTaskInput{
			TaskID: taskID, Name: body.Name, Prompt: body.Prompt, CronExpr: body.CronExpr,
			Timezone: body.Timezone, Enabled: body.Enabled, NextRunAt: next,
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownScheduledTask):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "scheduled task not found"})
			case errors.Is(err, store.ErrInvalidInput):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, prompt, cron_expr, timezone are required"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update scheduled task"})
			}
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

// deleteScheduledTask soft-deletes a scheduled task.
//
//	@Summary		Delete a scheduled task
//	@Description	Soft-deletes the scheduled task. Idempotent; caller must be a workspace member with edit rights.
//	@Tags			scheduled-tasks
//	@ID				deleteDevScheduledTask
//	@Param			taskID	path	string	true	"Scheduled task UUID"
//	@Success		204		"Scheduled task deleted"
//	@Failure		400		{object}	map[string]string	"task_id must be a valid uuid"
//	@Failure		403		{object}	map[string]string	"Caller is a viewer or not a workspace member"
//	@Failure		404		{object}	map[string]string	"Scheduled task not found"
//	@Failure		503		{object}	map[string]string	"Database-backed APIs are disabled"
//	@Router			/api/v1/scheduled-tasks/{taskID} [delete]
func deleteScheduledTask(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
		if !isUUID(taskID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id must be a valid uuid"})
			return
		}
		scope, ok := scheduledTaskScope(w, r.Context(), rs, taskID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, rs, scope.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		if err := rs.SoftDeleteScheduledTask(r.Context(), taskID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete scheduled task"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// runScheduledTaskNow triggers an out-of-band execution of the task.
//
//	@Summary		Trigger a scheduled task immediately
//	@Description	Enqueues an out-of-band execution of the task, independent of cron cadence. 409 if another run is already active for the same task.
//	@Tags			scheduled-tasks
//	@ID				runDevScheduledTaskNow
//	@Produce		json
//	@Param			taskID	path		string				true	"Scheduled task UUID"
//	@Success		202		{object}	map[string]string	"Run enqueued; response carries run_id"
//	@Failure		400		{object}	map[string]string	"task_id must be a valid uuid"
//	@Failure		403		{object}	map[string]string	"Caller is a viewer or not a workspace member"
//	@Failure		404		{object}	map[string]string	"Scheduled task not found"
//	@Failure		409		{object}	map[string]string	"A run is already active for this task"
//	@Failure		503		{object}	map[string]string	"Database-backed APIs are disabled"
//	@Router			/api/v1/scheduled-tasks/{taskID}/run-now [post]
func runScheduledTaskNow(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
		if !isUUID(taskID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id must be a valid uuid"})
			return
		}
		scope, ok := scheduledTaskScope(w, r.Context(), rs, taskID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, rs, scope.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		runID, err := rs.RunScheduledTaskNow(r.Context(), taskID)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrScheduledTaskBusy):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a run is already active for this task"})
			case errors.Is(err, store.ErrUnknownScheduledTask):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "scheduled task not found"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to trigger scheduled task"})
			}
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID})
	}
}
