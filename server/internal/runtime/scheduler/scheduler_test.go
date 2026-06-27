package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeStore struct {
	mu    sync.Mutex
	due   []store.DueScheduledTask
	fired []string
	res   map[string]store.FireScheduledTaskResult
}

func (f *fakeStore) ClaimDueScheduledTasks(ctx context.Context, now, staleBefore time.Time, claimedBy string, limit int32) ([]store.DueScheduledTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.due
	f.due = nil
	return out, nil
}

func (f *fakeStore) FireScheduledTaskRun(ctx context.Context, taskID string, nextRunAt time.Time) (store.FireScheduledTaskResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fired = append(f.fired, taskID)
	if r, ok := f.res[taskID]; ok {
		return r, nil
	}
	return store.FireScheduledTaskResult{RunID: "run-" + taskID}, nil
}

func TestSchedulerTickFiresDueTasks(t *testing.T) {
	f := &fakeStore{
		due: []store.DueScheduledTask{
			{ID: "t1", CronExpr: "0 9 * * *", Timezone: "UTC"},
			{ID: "t2", CronExpr: "*/5 * * * *", Timezone: "UTC"},
		},
		res: map[string]store.FireScheduledTaskResult{"t2": {Skipped: true, SkipReason: "self_overlap"}},
	}
	sc, err := New(f, Options{Interval: 15 * time.Second, ClaimedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	sc.tick(context.Background())
	ticks, fired, skipped, errs := sc.Stats()
	if ticks != 1 || fired != 1 || skipped != 1 || errs != 0 {
		t.Fatalf("stats: ticks=%d fired=%d skipped=%d errs=%d", ticks, fired, skipped, errs)
	}
	if len(f.fired) != 2 {
		t.Fatalf("expected 2 fire attempts, got %d", len(f.fired))
	}
}

func TestSchedulerRunStopsOnContextCancel(t *testing.T) {
	f := &fakeStore{}
	sc, _ := New(f, Options{Interval: 15 * time.Second, ClaimedBy: "test"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sc.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
