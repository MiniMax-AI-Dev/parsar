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

func listScheduledTasks(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		projectID, ok := projectIDForProjectAgent(w, r.Context(), rs, projectAgentID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberByProject(r, rs, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		tasks, err := rs.ListScheduledTasksByProjectAgent(r.Context(), projectAgentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list scheduled tasks"})
			return
		}
		writeJSON(w, http.StatusOK, tasks)
	}
}

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
		if err := requireWorkspaceMemberByProject(r, rs, scope.ProjectID); err != nil {
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
		if err := requireWorkspaceMemberByProject(r, rs, scope.ProjectID); err != nil {
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

func createScheduledTask(rs RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed APIs are disabled"})
			return
		}
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		projectID, ok := projectIDForProjectAgent(w, r.Context(), rs, projectAgentID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberNotViewerByProject(r, rs, projectID); err != nil {
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
			ProjectAgentID: projectAgentID,
			Name:           body.Name,
			Prompt:         body.Prompt,
			CronExpr:       body.CronExpr,
			Timezone:       body.Timezone,
			Enabled:        enabled,
			FeishuChatID:   body.FeishuChatID,
			CreatedBy:      actorID,
			NextRunAt:      next,
		})
		if err != nil {
			if errors.Is(err, store.ErrInvalidProjectInput) {
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
		if err := requireWorkspaceMemberNotViewerByProject(r, rs, scope.ProjectID); err != nil {
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
			case errors.Is(err, store.ErrInvalidProjectInput):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, prompt, cron_expr, timezone are required"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update scheduled task"})
			}
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

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
		if err := requireWorkspaceMemberNotViewerByProject(r, rs, scope.ProjectID); err != nil {
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
		if err := requireWorkspaceMemberNotViewerByProject(r, rs, scope.ProjectID); err != nil {
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
