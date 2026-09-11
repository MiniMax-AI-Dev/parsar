package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func TestTokenUsageDurableSnapshotsAndSessionTotals(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "usage"})
	if err != nil {
		t.Fatal(err)
	}
	usage := func(input int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"tokens":{"input_tokens":%d,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":2,"total_tokens":%d}}`, input, input+3))
	}
	check := func(raw json.RawMessage, input int) {
		t.Helper()
		var got v1.TokenUsage
		if json.Unmarshal(raw, &got) != nil || got.InputTokens != int64(input) || got.TotalTokens != int64(input+3) || got.InputTokensDetails.CachedTokens != 4 || got.OutputTokensDetails.ReasoningTokens != 2 {
			t.Fatalf("unexpected usage: %s", raw)
		}
	}
	for n, status := range []string{store.TurnFailed, store.TurnCancelled} {
		admission, err := s.SubmitMessage(ctx, tenant, session.ID, fmt.Sprint(n), json.RawMessage(`{"text":"measure"}`))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.TransitionTurn(ctx, tenant, session.ID, admission.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
		if err != nil {
			t.Fatal(err)
		}
		batch := []store.ExecutionEvent{{Kind: "usage", Payload: usage(10)}}
		for range 2 {
			if err = s.AppendTurnEvents(ctx, tenant, session.ID, admission.TurnID, 1, batch); err != nil {
				t.Fatal(err)
			}
		}
		// A later snapshot replaces the earlier measurement; it is not a delta.
		if err = s.AppendTurnEvents(ctx, tenant, session.ID, admission.TurnID, 2, []store.ExecutionEvent{{Kind: "usage", Payload: usage(20)}}); err != nil {
			t.Fatal(err)
		}
		measured, err := s.GetTurn(ctx, tenant, session.ID, admission.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		check(measured.Usage, 20)
		if _, err = s.CompleteExecution(ctx, tenant, session.ID, admission.TurnID, store.TurnCompleted, json.RawMessage(`{"done":{"usage":`+string(usage(99))+`}}`), "missing-binding", admission.Sequence); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		rolledBack, err := s.GetTurn(ctx, tenant, session.ID, admission.TurnID)
		if err != nil || rolledBack.Status != store.TurnInProgress {
			t.Fatalf("rollback: %+v %v", rolledBack, err)
		}
		check(rolledBack.Usage, 20)
		// Completion without usage retains the last persisted measurement.
		completed, err := s.CompleteExecution(ctx, tenant, session.ID, admission.TurnID, status, json.RawMessage(`{"done":{"content":"partial"}}`), "", admission.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		check(completed.Usage, 20)
		if _, err = s.CompleteExecution(ctx, tenant, session.ID, admission.TurnID, status, json.RawMessage(`{"done":{"usage":`+string(usage(99))+`}}`), "", admission.Sequence); !errors.Is(err, store.ErrTurnConflict) {
			t.Fatal(err)
		}
		if _, err = s.GetTurn(ctx, uuid.NewString(), session.ID, admission.TurnID); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
	}
	// A separate connection pool must recover the committed totals without engine state.
	restored, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	fresh := store.New(restored)
	got, err := fresh.GetSession(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var total v1.TokenUsage
	if err = json.Unmarshal(got.Usage, &total); err != nil {
		t.Fatal(err)
	}
	if total.InputTokens != 40 || total.OutputTokens != 6 || total.TotalTokens != 46 || total.InputTokensDetails.CachedTokens != 8 || total.OutputTokensDetails.ReasoningTokens != 4 {
		t.Fatalf("double counted totals: %+v", total)
	}
	page, err := fresh.ListSessions(ctx, tenant, "", 100, true)
	if err != nil || len(page.Sessions) != 1 || string(page.Sessions[0].Usage) != string(got.Usage) {
		t.Fatalf("list totals: %+v %v", page, err)
	}
	if _, err = fresh.GetSession(ctx, uuid.NewString(), session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}
