package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSessionEventsAreVisibleOnlyAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = sqlc.New(tx).AppendSessionEvent(ctx, sqlc.AppendSessionEventParams{ID: pgtype.UUID{Bytes: uuid.MustParse(session.ID), Valid: true}, Payload: []byte(`{"event":{"type":"agent.session.idle"}}`)}); err != nil {
			t.Fatal(err)
		}
		if changes, err := s.ListSessionEvents(ctx, tenant, session.ID, 0); err != nil || len(changes) != 0 {
			t.Fatal("uncommitted events were visible", changes, err)
		}
		if commit {
			err = tx.Commit(ctx)
		} else {
			err = tx.Rollback(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if changes, err := s.ListSessionEvents(ctx, tenant, session.ID, 0); err != nil || len(changes) != 1 || changes[0].Sequence != 1 {
		t.Fatal("commit or rollback changed sequence continuity", changes, err)
	}
}

func TestSessionEventsCommitSnapshotsRetriesAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "start", json.RawMessage(`{"text":"question"}`))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.SessionEventCursor(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitMessage(ctx, tenant, session.ID, "start", json.RawMessage(`{"text":"question"}`)); err != nil {
		t.Fatal(err)
	}
	after, _ := s.SessionEventCursor(ctx, tenant, session.ID)
	if before != after {
		t.Fatal("input retry published duplicate events")
	}
	if _, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	batch := []store.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"first","delta":"partial"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"cmd","stage":"before","native_item":{"id":"cmd","type":"commandExecution","command":"sleep 10","status":"inProgress"}}`)},
	}
	for range 2 {
		if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
			t.Fatal(err)
		}
	}
	before, _ = s.SessionEventCursor(ctx, tenant, session.ID)
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 3, []store.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"discarded","delta":"rollback"}`)},
		{Kind: "output_message", Payload: json.RawMessage(`{"status":"invalid"}`)},
	}); err == nil {
		t.Fatal("invalid projection accepted")
	}
	after, _ = s.SessionEventCursor(ctx, tenant, session.ID)
	if before != after {
		t.Fatal("failed transaction published events")
	}
	if _, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCancelled, json.RawMessage(`{"private":"must not escape"}`), "", input.Sequence); err != nil {
		t.Fatal(err)
	}
	var all []store.SessionChange
	cursor := int64(0)
	for {
		page, err := store.New(pool).ListSessionEvents(ctx, tenant, session.ID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		cursor = page[len(page)-1].Sequence
	}
	if all[0].Event.Type != "agent.session.turn.created" || all[0].Turn.Status != store.TurnQueued || all[len(all)-1].Event.Type != "agent.session.idle" || all[len(all)-1].Turn.Status != store.TurnCancelled {
		t.Fatalf("transition snapshots changed: %+v", all)
	}
	counts := map[string]int{}
	for _, change := range all {
		counts[change.Event.Type]++
		if change.Turn != nil && len(change.Turn.Outcome) > 0 && string(change.Turn.Outcome) != "null" {
			t.Fatal("raw outcome retained in public notification")
		}
		if change.Event.Type == "agent.session.turn.item.done" && change.Event.Item.Status != "incomplete" {
			t.Fatal("cancelled unfinished item reported complete")
		}
	}
	if counts["agent.session.turn.output_text.delta"] != 1 || counts["agent.session.turn.item.done"] != 2 {
		t.Fatal(counts)
	}
	if _, err = s.ListSessionEvents(ctx, uuid.NewString(), session.ID, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign event access", err)
	}
	before, _ = s.SessionEventCursor(ctx, tenant, session.ID)
	if _, err = pool.Exec(ctx, "UPDATE turns SET items_indexed=false WHERE id=$1", input.TurnID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListItems(ctx, tenant, session.ID, "", 100, true); err != nil {
		t.Fatal(err)
	}
	after, _ = s.SessionEventCursor(ctx, tenant, session.ID)
	if before != after {
		t.Fatal("history rebuilding published live events")
	}
}

func TestSessionEventsRetentionAndQueuedCancellation(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "retention"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DELETE FROM session_events WHERE session_id=$1", session.ID); err != nil {
			t.Error(err)
		}
	})
	inputs := make([]store.Input, 64)
	for i := range inputs {
		inputs[i] = store.Input{Kind: "message", Payload: json.RawMessage(`{"text":"input"}`)}
	}
	for range 5 {
		if _, err = s.SubmitInputs(ctx, tenant, session.ID, uuid.NewString(), inputs); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM session_events WHERE session_id=$1", session.ID).Scan(&count); err != nil || count != 256 {
		t.Fatal(count, err)
	}
	if _, err = s.ListSessionEvents(ctx, tenant, session.ID, 0); !errors.Is(err, store.ErrStreamGap) {
		t.Fatal("lagging reader did not detect missing events", err)
	}
	cursor, err := s.SessionEventCursor(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestCancel(ctx, tenant, session.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	changes, err := s.ListSessionEvents(ctx, tenant, session.ID, cursor)
	if err != nil || len(changes) != 2 || changes[0].Event.Type != "agent.session.turn.cancelled" || changes[1].Event.Type != "agent.session.idle" {
		t.Fatal(changes, err)
	}
	// Force byte retention independently of the event-count limit.
	if _, err = pool.Exec(ctx, "UPDATE session_events SET payload=jsonb_build_object('padding',repeat('x',524288)) WHERE session_id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListItems(ctx, tenant, session.ID, "", 1, true); err != nil {
		t.Fatal(err)
	}
	var bytes int64
	if err = pool.QueryRow(ctx, "SELECT sum(payload_bytes) FROM session_events WHERE session_id=$1", session.ID).Scan(&bytes); err != nil || bytes > 64*1024*1024 {
		t.Fatal(bytes, err)
	}
}
