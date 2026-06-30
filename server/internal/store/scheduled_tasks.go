package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// scheduledTaskFailureThreshold: consecutive failed runs before a task
// auto-disables. Spec §5 default = 5.
const scheduledTaskFailureThreshold = 5

// Scheduled-task error sentinels (kept local to this file; same package).
var (
	ErrUnknownScheduledTask = errors.New("unknown scheduled task")
	ErrScheduledTaskBusy    = errors.New("scheduled task already has an active run")
)

type ScheduledTaskRead struct {
	ID                  string     `json:"id"`
	ProjectAgentID      string     `json:"project_agent_id"`
	ConversationID      string     `json:"conversation_id"`
	Name                string     `json:"name"`
	Prompt              string     `json:"prompt"`
	CronExpr            string     `json:"cron_expr"`
	Timezone            string     `json:"timezone"`
	Enabled             bool       `json:"enabled"`
	FeishuChatID        string     `json:"feishu_chat_id"`
	FeishuChatName      string     `json:"feishu_chat_name"`
	NextRunAt           *time.Time `json:"next_run_at"`
	LastRunAt           *time.Time `json:"last_run_at"`
	LastRunID           string     `json:"last_run_id"`
	LastStatus          string     `json:"last_status"`
	ConsecutiveFailures int32      `json:"consecutive_failures"`
	CreatedBy           string     `json:"created_by"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateScheduledTaskInput: the handler resolves NextRunAt via the
// scheduler.NextRun cron helper and passes it in (store stays cron-free).
type CreateScheduledTaskInput struct {
	ProjectAgentID string
	Name           string
	Prompt         string
	CronExpr       string
	Timezone       string
	Enabled        bool
	FeishuChatID   string // "" = web only
	CreatedBy      string
	NextRunAt      time.Time
}

type UpdateScheduledTaskInput struct {
	TaskID    string
	Name      string
	Prompt    string
	CronExpr  string
	Timezone  string
	Enabled   bool
	NextRunAt time.Time
}

// ScheduledTaskScope is the RBAC resolution for a task id.
type ScheduledTaskScope struct {
	TaskID         string
	ProjectAgentID string
	ProjectID      string
	WorkspaceID    string
}

type ScheduledTaskRunRead struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	ProjectAgentID string     `json:"project_agent_id"`
	ConnectorType  string     `json:"connector_type"`
	Status         string     `json:"status"`
	FailureReason  string     `json:"failure_reason"`
	TriggerSource  string     `json:"trigger_source"`
	TriggerChannel string     `json:"trigger_channel"`
	TriggerRefID   string     `json:"trigger_ref_id"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// DueScheduledTask is the minimal payload the scheduler needs after a claim
// to compute the next fire time.
type DueScheduledTask struct {
	ID       string
	CronExpr string
	Timezone string
}

// FireScheduledTaskResult reports the outcome of a single cron fire. Exactly
// one of {RunID set, Skipped, Disabled} is meaningful per call.
type FireScheduledTaskResult struct {
	RunID      string
	Skipped    bool
	SkipReason string
	Disabled   bool
}

// textOrNull maps an empty string to a NULL pgtype.Text. (Named to avoid
// colliding with sandbox_bindings.go's nullableText, which has the inverse
// signature.)
func textOrNull(v string) pgtype.Text {
	v = strings.TrimSpace(v)
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}

func timePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

// scheduledRunTitle formats a per-run conversation title as
// "<task name> · MM-DD HH:mm" in the task's timezone (falling back to UTC for
// an unparseable zone). now is the dispatch time (UTC).
func scheduledRunTitle(name, timezone string, now time.Time) string {
	loc, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return fmt.Sprintf("%s · %s", name, now.In(loc).Format("01-02 15:04"))
}

func scheduledTaskFromCreateRow(r sqlc.CreateScheduledTaskRow) ScheduledTaskRead {
	return ScheduledTaskRead{
		ID:                  r.ID,
		ProjectAgentID:      r.ProjectAgentID,
		ConversationID:      r.ConversationID,
		Name:                r.Name,
		Prompt:              r.Prompt,
		CronExpr:            r.CronExpr,
		Timezone:            r.Timezone,
		Enabled:             r.Enabled,
		FeishuChatID:        r.FeishuChatID,
		FeishuChatName:      r.FeishuChatName,
		NextRunAt:           timePtr(r.NextRunAt),
		LastRunAt:           timePtr(r.LastRunAt),
		LastRunID:           r.LastRunID,
		LastStatus:          r.LastStatus,
		ConsecutiveFailures: r.ConsecutiveFailures,
		CreatedBy:           r.CreatedBy,
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
	}
}

func scheduledTaskFromGetRow(r sqlc.GetScheduledTaskRow) ScheduledTaskRead {
	return ScheduledTaskRead{
		ID:                  r.ID,
		ProjectAgentID:      r.ProjectAgentID,
		ConversationID:      r.ConversationID,
		Name:                r.Name,
		Prompt:              r.Prompt,
		CronExpr:            r.CronExpr,
		Timezone:            r.Timezone,
		Enabled:             r.Enabled,
		FeishuChatID:        r.FeishuChatID,
		FeishuChatName:      r.FeishuChatName,
		NextRunAt:           timePtr(r.NextRunAt),
		LastRunAt:           timePtr(r.LastRunAt),
		LastRunID:           r.LastRunID,
		LastStatus:          r.LastStatus,
		ConsecutiveFailures: r.ConsecutiveFailures,
		CreatedBy:           r.CreatedBy,
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
	}
}

func scheduledTaskFromListRow(r sqlc.ListScheduledTasksByProjectAgentRow) ScheduledTaskRead {
	return ScheduledTaskRead{
		ID:                  r.ID,
		ProjectAgentID:      r.ProjectAgentID,
		ConversationID:      r.ConversationID,
		Name:                r.Name,
		Prompt:              r.Prompt,
		CronExpr:            r.CronExpr,
		Timezone:            r.Timezone,
		Enabled:             r.Enabled,
		FeishuChatID:        r.FeishuChatID,
		FeishuChatName:      r.FeishuChatName,
		NextRunAt:           timePtr(r.NextRunAt),
		LastRunAt:           timePtr(r.LastRunAt),
		LastRunID:           r.LastRunID,
		LastStatus:          r.LastStatus,
		ConsecutiveFailures: r.ConsecutiveFailures,
		CreatedBy:           r.CreatedBy,
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
	}
}

func scheduledTaskFromListByProjectRow(r sqlc.ListScheduledTasksByProjectPageRow) ScheduledTaskRead {
	return ScheduledTaskRead{
		ID:                  r.ID,
		ProjectAgentID:      r.ProjectAgentID,
		ConversationID:      r.ConversationID,
		Name:                r.Name,
		Prompt:              r.Prompt,
		CronExpr:            r.CronExpr,
		Timezone:            r.Timezone,
		Enabled:             r.Enabled,
		FeishuChatID:        r.FeishuChatID,
		FeishuChatName:      r.FeishuChatName,
		NextRunAt:           timePtr(r.NextRunAt),
		LastRunAt:           timePtr(r.LastRunAt),
		LastRunID:           r.LastRunID,
		LastStatus:          r.LastStatus,
		ConsecutiveFailures: r.ConsecutiveFailures,
		CreatedBy:           r.CreatedBy,
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
	}
}

func scheduledTaskFromUpdateRow(r sqlc.UpdateScheduledTaskRow) ScheduledTaskRead {
	return ScheduledTaskRead{
		ID:                  r.ID,
		ProjectAgentID:      r.ProjectAgentID,
		ConversationID:      r.ConversationID,
		Name:                r.Name,
		Prompt:              r.Prompt,
		CronExpr:            r.CronExpr,
		Timezone:            r.Timezone,
		Enabled:             r.Enabled,
		FeishuChatID:        r.FeishuChatID,
		FeishuChatName:      r.FeishuChatName,
		NextRunAt:           timePtr(r.NextRunAt),
		LastRunAt:           timePtr(r.LastRunAt),
		LastRunID:           r.LastRunID,
		LastStatus:          r.LastStatus,
		ConsecutiveFailures: r.ConsecutiveFailures,
		CreatedBy:           r.CreatedBy,
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
	}
}

// CreateScheduledTask inserts the task row. No conversation is built up front:
// each fire/run-now creates its own fresh conversation (see
// dispatchScheduledRunTx), so conversation_id starts NULL and is backfilled
// with the most recent run's conversation after every dispatch.
func (s *Store) CreateScheduledTask(ctx context.Context, in CreateScheduledTaskInput) (ScheduledTaskRead, error) {
	var zero ScheduledTaskRead
	name := strings.TrimSpace(in.Name)
	prompt := strings.TrimSpace(in.Prompt)
	if name == "" || prompt == "" || strings.TrimSpace(in.CronExpr) == "" || strings.TrimSpace(in.Timezone) == "" {
		return zero, ErrInvalidProjectInput
	}
	if len(prompt) > 32000 {
		return zero, ErrInvalidProjectInput
	}
	createdBy, err := uuid(in.CreatedBy)
	if err != nil {
		return zero, err
	}
	// Validate the project_agent exists before anchoring a task to it.
	if _, err := s.GetProjectAgentDetail(ctx, in.ProjectAgentID); err != nil {
		return zero, err
	}
	now := time.Now().UTC()

	row, err := sqlc.New(s.db).CreateScheduledTask(ctx, sqlc.CreateScheduledTaskParams{
		ID:             mustUUID(newID()),
		ProjectAgentID: mustUUID(in.ProjectAgentID),
		Name:           name,
		Prompt:         prompt,
		CronExpr:       strings.TrimSpace(in.CronExpr),
		Timezone:       strings.TrimSpace(in.Timezone),
		Enabled:        in.Enabled,
		FeishuChatID:   textOrNull(in.FeishuChatID),
		FeishuChatName: pgtype.Text{},
		NextRunAt:      timestamptz(in.NextRunAt),
		CreatedBy:      createdBy,
		Now:            timestamptz(now),
	})
	if err != nil {
		return zero, err
	}
	return scheduledTaskFromCreateRow(row), nil
}

// GetScheduledTask returns a task by id; disabled tasks are included, only
// soft-deleted are hidden.
func (s *Store) GetScheduledTask(ctx context.Context, taskID string) (ScheduledTaskRead, error) {
	var zero ScheduledTaskRead
	id, err := uuid(taskID)
	if err != nil {
		return zero, err
	}
	row, err := sqlc.New(s.db).GetScheduledTask(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrUnknownScheduledTask
		}
		return zero, err
	}
	return scheduledTaskFromGetRow(row), nil
}

func (s *Store) ListScheduledTasksByProjectAgent(ctx context.Context, projectAgentID string) ([]ScheduledTaskRead, error) {
	id, err := uuid(projectAgentID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListScheduledTasksByProjectAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledTaskRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, scheduledTaskFromListRow(r))
	}
	return out, nil
}

// ListScheduledTasksByProjectResult bundles a page of scheduled tasks with the
// total row count for the project, so the standalone 定时任务 page can paginate.
type ListScheduledTasksByProjectResult struct {
	Tasks []ScheduledTaskRead
	Total int64
}

// ListScheduledTasksByProject is the project-wide counterpart to
// ListScheduledTasksByProjectAgent, powering the standalone 定时任务 page.
// Returns a newest-first page plus the total count under the same filter.
func (s *Store) ListScheduledTasksByProject(ctx context.Context, projectID string, limit, offset int32) (ListScheduledTasksByProjectResult, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if offset < 0 {
		offset = 0
	}
	id, err := uuid(projectID)
	if err != nil {
		return ListScheduledTasksByProjectResult{}, err
	}
	queries := sqlc.New(s.db)
	rows, err := queries.ListScheduledTasksByProjectPage(ctx, sqlc.ListScheduledTasksByProjectPageParams{
		ProjectID:  id,
		ItemLimit:  limit,
		ItemOffset: offset,
	})
	if err != nil {
		return ListScheduledTasksByProjectResult{}, err
	}
	total, err := queries.CountScheduledTasksByProject(ctx, id)
	if err != nil {
		return ListScheduledTasksByProjectResult{}, err
	}
	out := make([]ScheduledTaskRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, scheduledTaskFromListByProjectRow(r))
	}
	return ListScheduledTasksByProjectResult{Tasks: out, Total: total}, nil
}

func (s *Store) GetScheduledTaskScope(ctx context.Context, taskID string) (ScheduledTaskScope, error) {
	var zero ScheduledTaskScope
	id, err := uuid(taskID)
	if err != nil {
		return zero, err
	}
	row, err := sqlc.New(s.db).GetScheduledTaskScope(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrUnknownScheduledTask
		}
		return zero, err
	}
	return ScheduledTaskScope{
		TaskID:         row.ID,
		ProjectAgentID: row.ProjectAgentID,
		ProjectID:      row.ProjectID,
		WorkspaceID:    row.WorkspaceID,
	}, nil
}

func (s *Store) UpdateScheduledTask(ctx context.Context, in UpdateScheduledTaskInput) (ScheduledTaskRead, error) {
	var zero ScheduledTaskRead
	id, err := uuid(in.TaskID)
	if err != nil {
		return zero, err
	}
	name := strings.TrimSpace(in.Name)
	prompt := strings.TrimSpace(in.Prompt)
	if name == "" || prompt == "" || strings.TrimSpace(in.CronExpr) == "" || strings.TrimSpace(in.Timezone) == "" || len(prompt) > 32000 {
		return zero, ErrInvalidProjectInput
	}
	row, err := sqlc.New(s.db).UpdateScheduledTask(ctx, sqlc.UpdateScheduledTaskParams{
		ID:        id,
		Name:      name,
		Prompt:    prompt,
		CronExpr:  strings.TrimSpace(in.CronExpr),
		Timezone:  strings.TrimSpace(in.Timezone),
		Enabled:   in.Enabled,
		NextRunAt: timestamptz(in.NextRunAt),
		Now:       timestamptz(time.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrUnknownScheduledTask
		}
		return zero, err
	}
	return scheduledTaskFromUpdateRow(row), nil
}

// SoftDeleteScheduledTask marks the task deleted (idempotent).
func (s *Store) SoftDeleteScheduledTask(ctx context.Context, taskID string) error {
	id, err := uuid(taskID)
	if err != nil {
		return err
	}
	return sqlc.New(s.db).SoftDeleteScheduledTask(ctx, sqlc.SoftDeleteScheduledTaskParams{
		ID:  id,
		Now: timestamptz(time.Now().UTC()),
	})
}

func (s *Store) ListAgentRunsByScheduledTask(ctx context.Context, taskID string, limit int32) ([]ScheduledTaskRunRead, error) {
	id, err := uuid(taskID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := sqlc.New(s.db).ListAgentRunsByScheduledTask(ctx, sqlc.ListAgentRunsByScheduledTaskParams{TaskID: id, ItemLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledTaskRunRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, ScheduledTaskRunRead{
			ID:             r.ID,
			ConversationID: r.ConversationID,
			ProjectAgentID: r.ProjectAgentID,
			ConnectorType:  r.ConnectorType,
			Status:         r.Status,
			FailureReason:  r.FailureReason,
			TriggerSource:  r.TriggerSource,
			TriggerChannel: r.TriggerChannel,
			TriggerRefID:   r.TriggerRefID,
			CreatedAt:      r.CreatedAt.Time,
			StartedAt:      timePtr(r.StartedAt),
			FinishedAt:     timePtr(r.FinishedAt),
			UpdatedAt:      r.UpdatedAt.Time,
		})
	}
	return out, nil
}

// dispatchScheduledRunTx builds a fresh conversation, then writes the system
// trigger message + scheduled agent_run into it, all inside an open tx. It
// returns the new run id, the new conversation id (for the task's
// conversation_id backfill), and any streaming-dispatch inputs to flush AFTER
// commit. Shared by the cron path (FireScheduledTaskRun) and run-now.
func (s *Store) dispatchScheduledRunTx(ctx context.Context, q *sqlc.Queries, taskID, projectAgentID, taskName, timezone, prompt, createdBy string, now time.Time) (string, string, []StreamingDispatchInput, error) {
	// Resolve workspace first: GetProjectAgentRuntime guards on workspace_id,
	// so it can't double as the resolver. Runtime then yields project_id +
	// connector_type. No existing conversation to read — this builds its own.
	workspaceID, err := q.GetProjectAgentWorkspace(ctx, mustUUID(projectAgentID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, ErrUnknownProjectAgent
		}
		return "", "", nil, err
	}
	rt, err := q.GetProjectAgentRuntime(ctx, sqlc.GetProjectAgentRuntimeParams{ProjectAgentID: mustUUID(projectAgentID), WorkspaceID: mustUUID(workspaceID)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, ErrUnknownProjectAgent
		}
		return "", "", nil, err
	}

	// Fresh conversation per dispatch: primary_agent_id surfaces it in the
	// agent's 对话 list (ListProjectConversations filters on that metadata key).
	convID := newID()
	convMeta, _ := json.Marshal(map[string]any{
		"source":            "scheduled_task",
		"scheduled_task_id": taskID,
		"primary_agent_id":  projectAgentID,
	})
	if _, err := q.CreateProjectConversation(ctx, sqlc.CreateProjectConversationParams{
		ID:          mustUUID(convID),
		WorkspaceID: mustUUID(workspaceID),
		ProjectID:   mustUUID(rt.ProjectID),
		Surface:     "web",
		Form:        "thread",
		Title:       scheduledRunTitle(taskName, timezone, now),
		Metadata:    convMeta,
		Now:         timestamptz(now),
	}); err != nil {
		return "", "", nil, err
	}

	msgID := newID()
	msgMeta, _ := json.Marshal(map[string]any{"source": "scheduled_task", "scheduled_task_id": taskID})
	if err := q.CreateMessage(ctx, sqlc.CreateMessageParams{
		ID:             mustUUID(msgID),
		WorkspaceID:    mustUUID(workspaceID),
		ProjectID:      mustUUID(rt.ProjectID),
		ConversationID: mustUUID(convID),
		SenderType:     "system",
		SenderID:       pgtype.UUID{}, // null: system-authored
		Content:        prompt,
		Metadata:       msgMeta,
		Now:            timestamptz(now),
	}); err != nil {
		return "", "", nil, err
	}

	runID := newID()
	runMeta, _ := json.Marshal(map[string]any{"source": "scheduled_task", "scheduled_task_id": taskID})
	requestedBy := pgtype.UUID{}
	if v, err := uuid(createdBy); err == nil {
		requestedBy = v
	}
	if err := q.CreateScheduledAgentRun(ctx, sqlc.CreateScheduledAgentRunParams{
		ID:               mustUUID(runID),
		WorkspaceID:      mustUUID(workspaceID),
		ProjectID:        mustUUID(rt.ProjectID),
		ConversationID:   mustUUID(convID),
		TriggerMessageID: mustUUID(msgID),
		TriggerRefID:     mustUUID(taskID),
		RequestedByID:    requestedBy,
		ProjectAgentID:   mustUUID(projectAgentID),
		ConnectorType:    rt.ConnectorType,
		Metadata:         runMeta,
		Now:              timestamptz(now),
	}); err != nil {
		return "", "", nil, err
	}

	var pending []StreamingDispatchInput
	if connectorNeedsStreamingDispatch(rt.ConnectorType) {
		pending = append(pending, StreamingDispatchInput{RunID: runID, ConversationID: convID, ConnectorType: rt.ConnectorType})
	}
	return runID, convID, pending, nil
}

// FireScheduledTaskRun is the cron path: it row-locks the task, applies the
// self-overlap / failure-accounting / auto-disable rules, dispatches a run on
// success, and always advances next_run_at + releases the claim.
func (s *Store) FireScheduledTaskRun(ctx context.Context, taskID string, nextRunAt time.Time) (FireScheduledTaskResult, error) {
	var res FireScheduledTaskResult
	now := time.Now().UTC()
	tid, err := uuid(taskID)
	if err != nil {
		return res, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)

	task, err := q.GetScheduledTaskForUpdate(ctx, tid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, ErrUnknownScheduledTask
		}
		return res, err
	}
	if !task.Enabled {
		// Disabled between claim and fire: release + advance only.
		if err := q.AdvanceScheduledTaskAfterSkip(ctx, sqlc.AdvanceScheduledTaskAfterSkipParams{ID: tid, NextRunAt: timestamptz(nextRunAt), Now: timestamptz(now)}); err != nil {
			return res, err
		}
		if err := tx.Commit(ctx); err != nil {
			return res, err
		}
		res.Skipped = true
		res.SkipReason = "disabled"
		return res, nil
	}

	// Self-overlap: previous run still active → skip, advance only.
	switch task.LastRunStatus {
	case "queued", "running":
		if err := q.AdvanceScheduledTaskAfterSkip(ctx, sqlc.AdvanceScheduledTaskAfterSkipParams{ID: tid, NextRunAt: timestamptz(nextRunAt), Now: timestamptz(now)}); err != nil {
			return res, err
		}
		if err := tx.Commit(ctx); err != nil {
			return res, err
		}
		res.Skipped = true
		res.SkipReason = "self_overlap"
		return res, nil
	}

	// Failure accounting from the previous run's terminal status.
	failures := task.ConsecutiveFailures
	switch task.LastRunStatus {
	case "failed", "cancelled", "interrupted":
		failures++
	case "completed":
		failures = 0
	}
	if failures >= scheduledTaskFailureThreshold {
		if err := q.DisableScheduledTaskForFailures(ctx, sqlc.DisableScheduledTaskForFailuresParams{ID: tid, ConsecutiveFailures: failures, NextRunAt: timestamptz(nextRunAt), Now: timestamptz(now)}); err != nil {
			return res, err
		}
		if err := tx.Commit(ctx); err != nil {
			return res, err
		}
		res.Disabled = true
		return res, nil
	}

	runID, convID, pending, err := s.dispatchScheduledRunTx(ctx, q, task.ID, task.ProjectAgentID, task.Name, task.Timezone, task.Prompt, task.CreatedBy, now)
	if err != nil {
		return res, err
	}
	if err := q.MarkScheduledTaskDispatched(ctx, sqlc.MarkScheduledTaskDispatchedParams{
		ID: tid, LastRunID: mustUUID(runID), ConversationID: mustUUID(convID), ConsecutiveFailures: failures, NextRunAt: timestamptz(nextRunAt), Now: timestamptz(now),
	}); err != nil {
		return res, err
	}
	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	s.dispatchPendingStreaming(ctx, pending)
	res.RunID = runID
	return res, nil
}

// RunScheduledTaskNow is the out-of-band manual trigger: it does NOT touch
// next_run_at or consecutive_failures, but self-overlap still guards the
// shared work_dir (each fire builds its own fresh conversation).
func (s *Store) RunScheduledTaskNow(ctx context.Context, taskID string) (string, error) {
	now := time.Now().UTC()
	tid, err := uuid(taskID)
	if err != nil {
		return "", err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)

	task, err := q.GetScheduledTaskForUpdate(ctx, tid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrUnknownScheduledTask
		}
		return "", err
	}
	// run-now is allowed even when disabled, but self-overlap still guards the
	// shared work_dir.
	switch task.LastRunStatus {
	case "queued", "running":
		return "", ErrScheduledTaskBusy
	}
	runID, convID, pending, err := s.dispatchScheduledRunTx(ctx, q, task.ID, task.ProjectAgentID, task.Name, task.Timezone, task.Prompt, task.CreatedBy, now)
	if err != nil {
		return "", err
	}
	if err := q.MarkScheduledTaskRunNow(ctx, sqlc.MarkScheduledTaskRunNowParams{ID: tid, LastRunID: mustUUID(runID), ConversationID: mustUUID(convID), Now: timestamptz(now)}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	s.dispatchPendingStreaming(ctx, pending)
	return runID, nil
}

// ClaimDueScheduledTasks atomically claims due tasks for this pod
// (FOR UPDATE SKIP LOCKED + claim lease). Mirrors the feishu claim path.
func (s *Store) ClaimDueScheduledTasks(ctx context.Context, now, staleBefore time.Time, claimedBy string, limit int32) ([]DueScheduledTask, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := sqlc.New(s.db).ClaimDueScheduledTasks(ctx, sqlc.ClaimDueScheduledTasksParams{
		Now:         timestamptz(now),
		StaleBefore: timestamptz(staleBefore),
		ClaimedBy:   claimedBy,
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]DueScheduledTask, 0, len(rows))
	for _, r := range rows {
		out = append(out, DueScheduledTask{ID: r.ID, CronExpr: r.CronExpr, Timezone: r.Timezone})
	}
	return out, nil
}
