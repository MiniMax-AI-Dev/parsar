package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAgentRunEventsRespectPersistedCancellation(t *testing.T) {
	for _, terminal := range []string{"run.failed", "run.completed"} {
		for _, cancelled := range []bool{false, true} {
			name := terminal + "/active"
			if cancelled {
				name = terminal + "/cancelled"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				s := New(openTestDB(t))
				ids := mustSeedDevFixture(t, ctx, s)
				result, err := s.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
					ConversationID: ids.ConversationID, UserID: ids.UserID,
					Content: "terminal event check", MentionedAgentIDs: []string{ids.ProductAgentID},
				})
				if err != nil || len(result.RunIDs) != 1 {
					t.Fatalf("create run: %v, ids=%v", err, result.RunIDs)
				}
				runID := result.RunIDs[0]
				record := func(kind string) {
					t.Helper()
					if err := s.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{RunID: runID, EventKind: kind}); err != nil {
						t.Fatalf("record %s: %v", kind, err)
					}
				}
				record("run.started")
				want := []string{"run.started"}
				if cancelled {
					if ok, err := s.CancelAgentRun(ctx, runID, "user_cancelled"); err != nil || !ok {
						t.Fatalf("cancel run: %v, changed=%v", err, ok)
					}
					record("run.cancelled")
					want = append(want, "run.cancelled")
				}
				record(terminal)
				if !cancelled {
					want = append(want, terminal)
				}
				record("session.error")
				want = append(want, "session.error")
				events, err := s.ListAgentRunEvents(ctx, runID, 0)
				if err != nil || len(events) != len(want) {
					t.Fatalf("list events: %v, got %v, want %v", err, events, want)
				}
				for i, event := range events {
					if event.EventKind != want[i] || event.Sequence != int64(i+1) {
						t.Fatalf("event %d: kind=%s sequence=%d, want %s/%d", i, event.EventKind, event.Sequence, want[i], i+1)
					}
				}
			})
		}
	}
}

func TestTerminalEventLocksRunUntilCommit(t *testing.T) {
	ctx := context.Background()
	s := New(openTestDB(t))
	ids := mustSeedDevFixture(t, ctx, s)
	result, err := s.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID: ids.ConversationID, UserID: ids.UserID,
		Content: "concurrent cancellation check", MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil || len(result.RunIDs) != 1 {
		t.Fatalf("create run: %v, ids=%v", err, result.RunIDs)
	}
	runID := result.RunIDs[0]
	runUUID, err := uuid(runID)
	if err != nil {
		t.Fatal(err)
	}
	eventTx, err := beginTx(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer eventTx.Rollback(ctx)
	now := timestamptz(time.Now())
	_, err = sqlc.New(eventTx).InsertAgentRunEvent(ctx, sqlc.InsertAgentRunEventParams{
		AgentRunID: runUUID, Sequence: 1, EventKind: "run.failed", Payload: []byte(`{}`),
		OccurredAt: now, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelTx, err := beginTx(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelTx.Rollback(ctx)
	// A status-only UPDATE needs this lock, so cancellation cannot commit first.
	_, err = cancelTx.Exec(ctx, "select id from agent_runs where id = $1 for no key update nowait", runUUID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("status update should wait for event commit, got %v", err)
	}
	if err := cancelTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eventTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CancelAgentRun(ctx, runID, "user_cancelled"); err != nil || !ok {
		t.Fatalf("cancel after event commit: %v, changed=%v", err, ok)
	}
}

func TestTerminalAgentRunEventStillRejectsUnknownRun(t *testing.T) {
	s := New(openTestDB(t))
	err := s.RecordAgentRunEvent(context.Background(), RecordAgentRunEventInput{
		RunID: "00000000-0000-0000-0000-000000000099", EventKind: "run.failed",
	})
	if !errors.Is(err, ErrUnknownAgentRun) {
		t.Fatalf("unknown run error = %v", err)
	}
}
