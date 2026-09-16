package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestCommandOutputCommitsFragmentsSnapshotsAndRecovery(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	defer pool.Close()
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "command-output"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "start", json.RawMessage(`{"text":"run commands"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	event := func(kind, raw string) store.ExecutionEvent {
		return store.ExecutionEvent{Kind: kind, Payload: json.RawMessage(raw)}
	}
	batch := []store.ExecutionEvent{
		event("tool_call", `{"id":"cmd","stage":"before","observation":{"kind":"command","command":"run","status":"in_progress"}}`),
		event("command_output", `{"id":"cmd","delta":"same\n"}`),
		event("command_output", `{"id":"cmd","delta":"same\n"}`),
	}
	for range 2 {
		if err := s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.SessionEventCursor(ctx, tenant, session.ID)
	// A bad command reference rolls back preceding valid fragments and their events.
	if err := s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 4, []store.ExecutionEvent{
		event("command_output", `{"id":"cmd","delta":"rollback"}`),
		event("command_output", `{"id":"unknown","delta":"orphan"}`),
	}); err == nil {
		t.Fatal("unknown command accepted")
	}
	after, _ := s.SessionEventCursor(ctx, tenant, session.ID)
	if before != after {
		t.Fatal("rollback published output")
	}
	page, err := store.New(pool).ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("read draft: %+v %v", page, err)
	}
	if page.Items[1].Output != "same\nsame\n" {
		t.Fatal("draft output lost", page.Items[1])
	}
	final := []store.ExecutionEvent{
		event("tool_call", `{"id":"cmd","stage":"after","observation":{"kind":"command","command":"run","status":"completed","output":"authoritative","exit_code":0}}`),
		event("command_output", `{"id":"cmd","delta":"late"}`),
		event("tool_call", `{"id":"partial","stage":"before","observation":{"kind":"command","command":"wait","status":"in_progress"}}`),
		event("command_output", `{"id":"partial","delta":"已观察\n"}`),
	}
	if err := s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 4, final); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCancelled, json.RawMessage(`{}`), "", input.Sequence); err != nil {
		t.Fatal(err)
	}
	// Reopening the Store recovers committed Items without creating events.
	reopened := store.New(pool)
	before, _ = s.SessionEventCursor(ctx, tenant, session.ID)
	page, err = reopened.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("recovery: %+v %v", page, err)
	}
	if page.Items[1].Status != "completed" || page.Items[1].Output != "authoritative" || page.Items[2].Status != "incomplete" || page.Items[2].Output != "已观察\n" {
		t.Fatal("completion/cancellation lost command output", page.Items)
	}
	after, _ = s.SessionEventCursor(ctx, tenant, session.ID)
	if before != after {
		t.Fatal("query replayed events")
	}
	if _, err := reopened.ListSessionEvents(ctx, uuid.NewString(), session.ID, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign event access", err)
	}
	var fragments []string
	added, done := map[string]bool{}, map[string]bool{}
	indexes := map[string]int32{}
	cursor := int64(0)
	for {
		changes, err := reopened.ListSessionEvents(ctx, tenant, session.ID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) == 0 {
			break
		}
		for _, change := range changes {
			e := change.Event
			if e.Item != nil && e.Item.Type == "command_execution" {
				if e.Type == "agent.session.turn.item.added" {
					added[e.Item.ID] = true
					indexes[e.Item.ID] = *e.OutputIndex
				}
				if e.Type == "agent.session.turn.item.done" {
					done[e.Item.ID] = true
				}
			}
			if e.Type == "agent.output.command_execution_output.delta" {
				if !added[e.ItemID] || done[e.ItemID] || e.OutputIndex == nil || *e.OutputIndex != indexes[e.ItemID] || e.TurnID != input.TurnID || e.SessionID != session.ID || e.EventID == "" {
					t.Fatal("invalid command delta identity/order", e)
				}
				fragments = append(fragments, *e.Delta)
			}
			cursor = change.Sequence
		}
	}
	if !reflect.DeepEqual(fragments, []string{"same\n", "same\n", "已观察\n"}) || len(done) != 2 {
		t.Fatal("incorrect live fragments", fragments, done)
	}
}

func TestExecutionJournalsCommandOutputBeforeCancellation(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	input := h.message("command", "run a command")
	result := h.run(ctx, input.TurnID)
	h.read(proto.TypePromptRequest)
	h.write(input.TurnID, proto.TypeToolCall, proto.ToolCallPayload{ID: "cmd", Stage: "before", Observation: &proto.ToolObservation{Kind: "command", Command: "wait", Status: "in_progress"}})
	h.write(input.TurnID, proto.TypeCommandOutput, proto.CommandOutputPayload{ID: "cmd", Delta: "partial"})
	if _, err := h.s.RequestCancel(ctx, h.tenant, h.session.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	env := h.read(proto.TypePromptCancel)
	var cancel proto.PromptCancelPayload
	if err := env.DecodePayload(&cancel); err != nil {
		t.Fatal(err)
	}
	h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true, Outcome: &proto.DonePayload{}})
	h.finished(result, store.TurnCancelled)
	page, err := h.s.ListItems(ctx, h.tenant, h.session.ID, "", 100, true)
	if err != nil || len(page.Items) != 2 || page.Items[1].Status != "incomplete" || page.Items[1].Output != "partial" {
		t.Fatalf("journal/cancellation lost partial output: %+v %v", page, err)
	}
}
