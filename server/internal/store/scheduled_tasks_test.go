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
	if created.ConversationID == "" {
		t.Fatal("expected container conversation id")
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
