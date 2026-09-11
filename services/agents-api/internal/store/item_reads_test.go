package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestItemsRecoverSnapshotsPartialResultsPaginationAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "items"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "first", json.RawMessage(`{"text":"question"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	batch := []store.ExecutionEvent{
		{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"in_progress"}`)},
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"answer","delta":"draft"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"cmd","stage":"before","native_item":{"id":"cmd","type":"commandExecution","command":"exit 7","status":"inProgress"}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"cmd","stage":"after","native_item":{"id":"cmd","type":"commandExecution","command":"exit 7","status":"failed","aggregatedOutput":"expected failure","exitCode":7}}`)},
		{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"corrected answer","phase":"final_answer"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"mcp","stage":"after","native_item":{"id":"mcp","type":"mcpToolCall","server":"reference","tool":"lookup","arguments":{},"status":"completed","result":{"structuredContent":{"number":9007199254740993}}}}`)},
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"partial","delta":"unfinished"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"waiting","stage":"before","native_item":{"id":"waiting","type":"commandExecution","command":"sleep 10","status":"inProgress"}}`)},
		{Kind: "done", Payload: json.RawMessage(`{"content":"corrected answer","metadata":{"agent_session_id":"PRIVATE"}}`)},
	}
	for range 2 {
		if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 6 {
		t.Fatalf("wrong count: %+v", page)
	}
	if page.Items[4].Status != "in_progress" || page.Items[5].Status != "in_progress" {
		t.Fatal(page.Items)
	}
	_, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCancelled, json.RawMessage(`{}`), "", input.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	reopened := store.New(pool)
	page, err = reopened.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"completed", "completed", "failed", "completed", "incomplete", "incomplete"}
	for i, item := range page.Items {
		if item.Status != want[i] {
			t.Fatalf("item %d: %+v", i, item)
		}
	}
	if *page.Items[1].Content[0].Text != "corrected answer" || *page.Items[2].ExitCode != 7 {
		t.Fatal(page.Items)
	}
	raw, _ := json.Marshal(page.Items)
	if strings.Contains(string(raw), "PRIVATE") || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal(string(raw))
	}
	for _, asc := range []bool{true, false} {
		var all []v1.Item
		cursor := ""
		for {
			next, err := reopened.ListItems(ctx, tenant, session.ID, cursor, 2, asc)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, next.Items...)
			if !next.HasMore {
				break
			}
			cursor = next.Items[len(next.Items)-1].ID
		}
		for i, item := range all {
			j := i
			if !asc {
				j = len(all) - 1 - i
			}
			if !reflect.DeepEqual(item, page.Items[j]) {
				t.Fatal("pagination changed items")
			}
		}
	}
	other, _ := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "other"})
	for _, scope := range []struct{ tenant, session, cursor string }{{uuid.NewString(), session.ID, ""}, {tenant, other.ID, page.Items[0].ID}} {
		if _, err = s.ListItems(ctx, scope.tenant, scope.session, scope.cursor, 20, true); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
	}
	// Simulate upgrading persisted journal history; reindex must preserve IDs and content.
	if _, err = pool.Exec(ctx, "DELETE FROM session_items WHERE session_id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE turns SET items_indexed=false WHERE session_id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rebuilt, page) {
		t.Fatalf("history changed after rebuilding: %+v", rebuilt)
	}
}

func TestItemProjectionFailureRollsBackJournalAndLegacyAggregateRecovers(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, _ := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "legacy"})
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "input", json.RawMessage(`{"text":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	bad := []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"must roll back"}`)}, {Kind: "tool_call", Payload: json.RawMessage(`{"id":"mismatch","stage":"after","native_item":{"id":"other","type":"commandExecution"}}`)}}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, bad); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	events, err := s.ListTurnEvents(ctx, tenant, session.ID, input.TurnID, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
	page, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnInProgress, Status: store.TurnCompleted, Outcome: json.RawMessage(`{"done":{"content":"legacy answer","metadata":{"private":"SECRET"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(current.Items) != 2 || *current.Items[1].Content[0].Text != "legacy answer" {
		t.Fatal(current, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE turns SET items_indexed=false WHERE id=$1", input.TurnID); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	if *page.Items[1].Content[0].Text != "legacy answer" {
		t.Fatal(page)
	}
}

func TestReceiptOnlyTextRecoversWithoutInventingCompletion(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	for _, receiptOnly := range []bool{true, false} {
		session, _ := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: uuid.NewString()})
		input, err := s.SubmitMessage(ctx, tenant, session.ID, "first", json.RawMessage(`{"text":"test"}`))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
		if err != nil {
			t.Fatal(err)
		}
		if receiptOnly {
			err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, []store.ExecutionEvent{{Kind: "cancel_receipt", Payload: json.RawMessage(`{"applied":true,"outcome":{"content":"retained cancellation text"}}`)}})
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCancelled, json.RawMessage(`{"done":{"content":"retained cancellation text"}}`), "", input.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		page, err := store.New(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
		if err != nil || len(page.Items) != 2 || page.Items[1].Status != "incomplete" || *page.Items[1].Content[0].Text != "retained cancellation text" {
			t.Fatal(page, err)
		}
		if _, err = pool.Exec(ctx, "DELETE FROM session_items WHERE session_id=$1", session.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, "UPDATE turns SET items_indexed=false WHERE id=$1", input.TurnID); err != nil {
			t.Fatal(err)
		}
		rebuilt, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
		if err != nil || !reflect.DeepEqual(page, rebuilt) {
			t.Fatal(rebuilt, err)
		}
	}
}

func TestLegacyFailureRetainsPartialAnswerAcrossRecovery(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "failed-items"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "first", json.RawMessage(`{"text":"question"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	batch := []store.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"delta":"partial answer"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"open","stage":"after","native_item":{"id":"open","type":"webSearch","action":{"type":"openPage","url":"https://example.com"}}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"find","stage":"after","native_item":{"id":"find","type":"webSearch","action":{"type":"findInPage","url":"https://example.com","pattern":"needle"}}}`)},
		{Kind: "error", Payload: json.RawMessage(`{"error":"provider failure"}`)},
		{Kind: "done", Payload: json.RawMessage(`{"content":"provider failure"}`)},
	}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
		t.Fatal(err)
	}
	_, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnFailed, json.RawMessage(`{"done":{"content":"provider failure"},"error_code":"engine_failed"}`), "", input.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.New(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 4 {
		t.Fatal(page, err)
	}
	if page.Items[1].Status != "incomplete" || *page.Items[1].Content[0].Text != "partial answer" || page.Items[2].Action.Type != "open_page" || page.Items[3].Action.Type != "find_in_page" {
		t.Fatal(page)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM session_items WHERE session_id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE turns SET items_indexed=false WHERE id=$1", input.TurnID); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || !reflect.DeepEqual(page, rebuilt) {
		t.Fatal(rebuilt, err)
	}
}
