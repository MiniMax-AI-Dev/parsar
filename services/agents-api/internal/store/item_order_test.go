package store_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestItemObservationOrderSurvivesTiesUpdatesRetriesAndRecovery(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "ordered"})
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
	page, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	want := []string{page.Items[0].ID}
	keys := []string{"first", "second", "third"}
	// Oppose UUID order so a timestamp tie cannot accidentally pass this check.
	sort.Slice(keys, func(i, j int) bool {
		return items.Identity(input.TurnID, "message:"+keys[i]) > items.Identity(input.TurnID, "message:"+keys[j])
	})
	var batch []store.ExecutionEvent
	for _, key := range keys {
		payload, _ := json.Marshal(map[string]string{"id": key, "status": "in_progress"})
		batch = append(batch, store.ExecutionEvent{Kind: "output_message", Payload: payload})
		want = append(want, items.Identity(input.TurnID, "message:"+key))
	}
	for range 2 {
		if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SubmitMessage(ctx, tenant, session.ID, "steer", json.RawMessage(`{"text":"continue"}`)); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 5 {
		t.Fatal(page, err)
	}
	want = append(want, page.Items[4].ID)
	completion, _ := json.Marshal(map[string]string{"id": keys[0], "status": "completed", "text": "final"})
	batch = []store.ExecutionEvent{{Kind: "output_message", Payload: completion}, {Kind: "delta", Payload: json.RawMessage(`{"item_id":"last","delta":"partial"}`)}}
	for range 2 {
		if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 4, batch); err != nil {
			t.Fatal(err)
		}
	}
	want = append(want, items.Identity(input.TurnID, "message:last"))
	bad := []store.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"rollback","delta":"discard"}`)},
		{Kind: "output_message", Payload: json.RawMessage(`{"id":"invalid","status":"invalid"}`)},
	}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 6, bad); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 6, []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"item_id":"after-rollback","delta":"retained"}`)}}); err != nil {
		t.Fatal(err)
	}
	want = append(want, items.Identity(input.TurnID, "message:after-rollback"))
	if _, err = pool.Exec(ctx, "UPDATE session_items SET created_at='2026-01-01' WHERE session_id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	checkOrder := func() {
		t.Helper()
		for _, asc := range []bool{true, false} {
			var got []string
			cursor := ""
			for {
				page, err := store.New(pool).ListItems(ctx, tenant, session.ID, cursor, 2, asc)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page.Items {
					got = append(got, item.ID)
				}
				if !page.HasMore {
					break
				}
				cursor = page.Items[len(page.Items)-1].ID
			}
			if !asc {
				for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
					got[i], got[j] = got[j], got[i]
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("observation order = %v; want %v", got, want)
			}
		}
	}
	checkOrder()
	for position, id := range want {
		var storedPosition int
		var output pgtype.Int4
		if err = pool.QueryRow(ctx, "SELECT position, output_index FROM session_items WHERE id=$1", id).Scan(&storedPosition, &output); err != nil {
			t.Fatal(err)
		}
		if storedPosition != position || output.Valid != (position != 0 && position != 4) {
			t.Fatalf("item %d: position=%d output=%+v", position, storedPosition, output)
		}
		index := position - 1
		if position > 4 {
			index--
		}
		if output.Valid && int(output.Int32) != index {
			t.Fatalf("output index = %d, want %d", output.Int32, index)
		}
	}
	if _, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCancelled, json.RawMessage(`{}`), "", input.Sequence); err != nil {
		t.Fatal(err)
	}
	checkOrder()
	next, err := s.SubmitMessage(ctx, tenant, session.ID, "next-turn", json.RawMessage(`{"text":"new turn"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionTurn(ctx, tenant, session.ID, next.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, next.TurnID, 1, []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"item_id":"new","delta":"new turn"}`)}}); err != nil {
		t.Fatal(err)
	}
	var index int
	if err = pool.QueryRow(ctx, "SELECT output_index FROM session_items WHERE id=$1", items.Identity(next.TurnID, "message:new")).Scan(&index); err != nil || index != 0 {
		t.Fatalf("new Turn output index=%d: %v", index, err)
	}
}
