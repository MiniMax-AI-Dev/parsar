package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

type seededAgent struct {
	ProjectAgentID string
	UserID         string
}

func seedProjectAgent(t *testing.T, s *Store) seededAgent {
	t.Helper()
	ids := DefaultDevFixtureIDs()
	if _, err := s.SeedDevFixture(context.Background()); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	return seededAgent{ProjectAgentID: ids.BackendProjectAgentID, UserID: ids.UserID}
}

func markRunStatus(t *testing.T, s *Store, runID, status string) {
	t.Helper()
	if _, err := s.db.Exec(context.Background(), `update agent_runs set status=$2, updated_at=now() where id=$1::uuid`, runID, status); err != nil {
		t.Fatalf("markRunStatus: %v", err)
	}
}

func TestScheduledTaskCRUDRoundtrip(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	pa := seedProjectAgent(t, s)

	created, err := s.CreateScheduledTask(ctx, CreateScheduledTaskInput{
		ProjectAgentID: pa.ProjectAgentID,
		Name:           "nightly",
		Prompt:         "summarize today",
		CronExpr:       "0 9 * * *",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
		CreatedBy:      pa.UserID,
		NextRunAt:      time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ConversationID != "" {
		t.Fatalf("expected no conversation at creation, got %q", created.ConversationID)
	}
	got, err := s.GetScheduledTask(ctx, created.ID)
	if err != nil || got.Name != "nightly" {
		t.Fatalf("get: %v %+v", err, got)
	}
	list, err := s.ListScheduledTasksByProjectAgent(ctx, pa.ProjectAgentID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	scope, err := s.GetScheduledTaskScope(ctx, created.ID)
	if err != nil || scope.ProjectAgentID != pa.ProjectAgentID {
		t.Fatalf("scope: %v %+v", err, scope)
	}
}

func TestScheduledTaskUpdateAndSoftDelete(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	pa := seedProjectAgent(t, s)
	created, err := s.CreateScheduledTask(ctx, CreateScheduledTaskInput{ProjectAgentID: pa.ProjectAgentID, Name: "a", Prompt: "p", CronExpr: "0 9 * * *", Timezone: "UTC", Enabled: true, CreatedBy: pa.UserID, NextRunAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	upd, err := s.UpdateScheduledTask(ctx, UpdateScheduledTaskInput{TaskID: created.ID, Name: "b", Prompt: "p2", CronExpr: "0 10 * * *", Timezone: "UTC", Enabled: false, NextRunAt: time.Now().UTC().Add(2 * time.Hour)})
	if err != nil || upd.Name != "b" || upd.Enabled {
		t.Fatalf("update: %v %+v", err, upd)
	}
	if err := s.SoftDeleteScheduledTask(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetScheduledTask(ctx, created.ID); !errors.Is(err, ErrUnknownScheduledTask) {
		t.Fatalf("expected ErrUnknownScheduledTask after delete, got %v", err)
	}
}

func TestScheduledTaskTimelineHandlesNullSenderID(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	pa := seedProjectAgent(t, s)

	created, err := s.CreateScheduledTask(ctx, CreateScheduledTaskInput{
		ProjectAgentID: pa.ProjectAgentID,
		Name:           "reminder",
		Prompt:         "drink water",
		CronExpr:       "0 9 * * *",
		Timezone:       "UTC",
		Enabled:        true,
		CreatedBy:      pa.UserID,
		NextRunAt:      time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Firing writes the trigger message with sender_type='system' and a
	// NULL sender_id (system-authored). The timeline read must tolerate that
	// NULL instead of failing the row scan.
	fired, err := s.FireScheduledTaskRun(ctx, created.ID, time.Now().UTC().Add(time.Hour))
	if err != nil || fired.RunID == "" {
		t.Fatalf("fire: %v %+v", err, fired)
	}

	// The fire builds its own conversation and backfills it onto the task; read
	// the timeline through that backfilled id.
	got, err := s.GetScheduledTask(ctx, created.ID)
	if err != nil || got.ConversationID == "" {
		t.Fatalf("get after fire: %v conv=%q", err, got.ConversationID)
	}
	timeline, err := s.GetConversationTimeline(ctx, got.ConversationID, 100)
	if err != nil {
		t.Fatalf("timeline must not fail on system message with NULL sender_id: %v", err)
	}
	if len(timeline.Messages) == 0 {
		t.Fatalf("expected the system trigger message in timeline, got %+v", timeline.Messages)
	}
	first := timeline.Messages[0]
	if first.SenderType != "system" {
		t.Fatalf("expected first message sender_type=system, got %q", first.SenderType)
	}
	if first.SenderID != "" {
		t.Fatalf("expected empty sender_id for system message, got %q", first.SenderID)
	}
}

func TestFireScheduledTaskSelfOverlapSkips(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	pa := seedProjectAgent(t, s)
	created, err := s.CreateScheduledTask(ctx, CreateScheduledTaskInput{ProjectAgentID: pa.ProjectAgentID, Name: "a", Prompt: "p", CronExpr: "0 9 * * *", Timezone: "UTC", Enabled: true, CreatedBy: pa.UserID, NextRunAt: time.Now().UTC().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}

	r1, err := s.FireScheduledTaskRun(ctx, created.ID, time.Now().UTC().Add(time.Hour))
	if err != nil || r1.RunID == "" {
		t.Fatalf("first fire: %v %+v", err, r1)
	}
	r2, err := s.FireScheduledTaskRun(ctx, created.ID, time.Now().UTC().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Skipped || r2.SkipReason != "self_overlap" {
		t.Fatalf("expected self_overlap skip, got %+v", r2)
	}
}

func TestFireScheduledTaskAutoDisablesAtThreshold(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	pa := seedProjectAgent(t, s)
	created, err := s.CreateScheduledTask(ctx, CreateScheduledTaskInput{ProjectAgentID: pa.ProjectAgentID, Name: "a", Prompt: "p", CronExpr: "0 9 * * *", Timezone: "UTC", Enabled: true, CreatedBy: pa.UserID, NextRunAt: time.Now().UTC().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	// Each iteration dispatches a run, then forces it to "failed". The disable
	// check fires when prior consecutive_failures + this run's failure reaches
	// the threshold, so it takes threshold+1 fires to trip.
	for i := 0; i <= scheduledTaskFailureThreshold; i++ {
		r, err := s.FireScheduledTaskRun(ctx, created.ID, time.Now().UTC().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if r.Disabled {
			got, err := s.GetScheduledTask(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Enabled {
				t.Fatal("expected disabled")
			}
			return
		}
		markRunStatus(t, s, r.RunID, "failed")
	}
	t.Fatal("expected task to auto-disable within threshold+1 iterations")
}

func TestScheduledTaskFireCreatesFreshConversation(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	pa := seedProjectAgent(t, s)

	created, err := s.CreateScheduledTask(ctx, CreateScheduledTaskInput{
		ProjectAgentID: pa.ProjectAgentID,
		Name:           "pulse",
		Prompt:         "ping",
		CronExpr:       "0 9 * * *",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
		CreatedBy:      pa.UserID,
		NextRunAt:      time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ConversationID != "" {
		t.Fatalf("expected no conversation at creation, got %q", created.ConversationID)
	}

	// Fire, then move the run to a terminal state so the second fire isn't
	// skipped as a self-overlap.
	f1, err := s.FireScheduledTaskRun(ctx, created.ID, time.Now().UTC().Add(time.Hour))
	if err != nil || f1.RunID == "" {
		t.Fatalf("fire 1: %v %+v", err, f1)
	}
	markRunStatus(t, s, f1.RunID, "completed")

	f2, err := s.FireScheduledTaskRun(ctx, created.ID, time.Now().UTC().Add(2*time.Hour))
	if err != nil || f2.RunID == "" {
		t.Fatalf("fire 2: %v %+v", err, f2)
	}

	runs, err := s.ListAgentRunsByScheduledTask(ctx, created.ID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	convByRun := map[string]string{}
	for _, r := range runs {
		if r.ConversationID == "" {
			t.Fatalf("run %s has empty conversation_id", r.ID)
		}
		convByRun[r.ID] = r.ConversationID
	}
	if convByRun[f1.RunID] == convByRun[f2.RunID] {
		t.Fatalf("expected a distinct conversation per fire, both = %q", convByRun[f1.RunID])
	}

	// Each conversation holds exactly its own single trigger message and carries
	// primary_agent_id so it surfaces in the agent's 对话 list.
	for runID, convID := range convByRun {
		timeline, err := s.GetConversationTimeline(ctx, convID, 100)
		if err != nil {
			t.Fatalf("timeline for run %s: %v", runID, err)
		}
		if len(timeline.Messages) != 1 {
			t.Fatalf("expected 1 trigger message in conv %s, got %d", convID, len(timeline.Messages))
		}
		var primaryAgent string
		if err := s.db.QueryRow(ctx, `select coalesce(metadata->>'primary_agent_id','') from conversations where id=$1::uuid`, convID).Scan(&primaryAgent); err != nil {
			t.Fatalf("read conv metadata %s: %v", convID, err)
		}
		if primaryAgent != pa.ProjectAgentID {
			t.Fatalf("expected primary_agent_id=%q on conv %s, got %q", pa.ProjectAgentID, convID, primaryAgent)
		}
	}

	// The task's conversation_id is backfilled with the latest fire's conv.
	got, err := s.GetScheduledTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ConversationID != convByRun[f2.RunID] {
		t.Fatalf("expected task.conversation_id backfilled to latest fire %q, got %q", convByRun[f2.RunID], got.ConversationID)
	}
}
