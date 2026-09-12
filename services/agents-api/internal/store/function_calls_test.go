package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func functionCallFixture(id string) FunctionCall {
	return FunctionCall{CallID: id, ExecutorCallID: "native-" + id, Name: "lookup", Arguments: json.RawMessage(`{"ticket":9007199254740993}`)}
}

func TestFunctionCallsPersistCompleteResultsAndReceipts(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	transition(t, s, tenant, session.ID, turn, TurnInProgress, TurnWaiting)
	results := []string{
		`{"success":true,"output":"answer"}`,
		`{"success":true,"output":""}`,
		`{"success":false,"output":[{"type":"input_text","text":"before"},{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":""}],"error":"tool failed"}`,
		`{"success":true,"output":[],"error":null}`,
		`{"success":false,"output":null,"error":"missing"}`,
		`{"success":false}`,
	}
	for i, raw := range results {
		call := functionCallFixture(fmt.Sprint(i))
		if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, call); err != nil {
			t.Fatal(err)
		}
		retry := call
		retry.Arguments = json.RawMessage(`{ "ticket" : 9007199254740993 }`)
		if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, retry); err != nil {
			t.Fatal(err)
		}
		if err := s.ConfirmFunctionResult(t.Context(), tenant, session.ID, turn, call.CallID); !errors.Is(err, ErrTurnConflict) {
			t.Fatal("unsubmitted result applied", err)
		}
		if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, call.CallID, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.PendingFunctionCalls(t.Context(), tenant, session.ID, turn)
	if err != nil || len(before) != len(results) {
		t.Fatal(before, err)
	}
	pool.Close()
	reopened, _ := testStore(t)
	restored, err := reopened.PendingFunctionCalls(t.Context(), tenant, session.ID, turn)
	if err != nil || !reflect.DeepEqual(before, restored) {
		t.Fatal("recovery lost calls/results", restored, err)
	}
	for i, expected := range results {
		id := fmt.Sprint(i)
		row, err := reopened.GetFunctionCall(t.Context(), tenant, session.ID, turn, id)
		if err != nil || row.Applied || row.ExecutorCallID != "native-"+id || !strings.Contains(string(row.Arguments), "9007199254740993") {
			t.Fatal(row, err)
		}
		normalized, err := canonicalJSONObject(row.Result)
		wanted, _ := canonicalJSONObject(json.RawMessage(expected))
		if err != nil || string(normalized) != string(wanted) {
			t.Fatal("result changed", string(row.Result), err)
		}
		if err := reopened.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, id, json.RawMessage(expected)); err != nil {
			t.Fatal(err)
		}
		if err := reopened.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, id, json.RawMessage(`{"success":false,"error":"changed"}`)); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal(err)
		}
		for range 2 {
			if err := reopened.ConfirmFunctionResult(t.Context(), tenant, session.ID, turn, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	pending, err := reopened.PendingFunctionCalls(t.Context(), tenant, session.ID, turn)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	again, _ := testStore(t)
	row, err := again.GetFunctionCall(t.Context(), tenant, session.ID, turn, "0")
	if err != nil || !row.Applied {
		t.Fatal("receipt did not persist", row, err)
	}
}

func TestFunctionCallsAreScopedAndImmutable(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	call := functionCallFixture("call")
	if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, call); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("queued turn accepted callback", err)
	}
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, call); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"call", "executor", "name", "arguments"} {
		changed := call
		switch field {
		case "call":
			changed.CallID = "another-public-id"
		case "executor":
			changed.ExecutorCallID = "another-executor-id"
		case "name":
			changed.Name = "another-function"
		case "arguments":
			changed.Arguments = json.RawMessage(`{"ticket":9007199254740992}`)
		}
		if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, changed); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal(field, err)
		}
	}
	for _, scope := range []struct{ tenant, session, turn string }{
		{uuid.NewString(), session.ID, turn}, {tenant, uuid.NewString(), turn}, {tenant, session.ID, uuid.NewString()},
	} {
		_, err := s.GetFunctionCall(t.Context(), scope.tenant, scope.session, scope.turn, "call")
		if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := s.PendingFunctionCalls(t.Context(), scope.tenant, scope.session, scope.turn); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if err := s.RecordFunctionCall(t.Context(), scope.tenant, scope.session, scope.turn, call); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if err := s.SubmitFunctionResult(t.Context(), scope.tenant, scope.session, scope.turn, "call", json.RawMessage(`{"success":true}`)); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if err := s.ConfirmFunctionResult(t.Context(), scope.tenant, scope.session, scope.turn, "call"); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, "missing", json.RawMessage(`{"success":true}`)); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFunctionResultsCannotApplyAfterCancellationOrCompletion(t *testing.T) {
	for _, terminal := range []string{TurnCancelled, TurnCompleted, TurnFailed} {
		t.Run(terminal, func(t *testing.T) {
			s, _ := testStore(t)
			tenant, session := newTurnSession(t, s)
			turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
			transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
			for _, id := range []string{"submitted", "pending"} {
				if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, functionCallFixture(id)); err != nil {
					t.Fatal(err)
				}
			}
			result := json.RawMessage(`{"success":true,"output":"saved"}`)
			if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, "submitted", result); err != nil {
				t.Fatal(err)
			}
			if terminal == TurnCancelled {
				if _, err := s.RequestCancel(t.Context(), tenant, session.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				assertNoPendingFunctions(t, s, tenant, session.ID, turn)
				if err := s.ConfirmFunctionResult(t.Context(), tenant, session.ID, turn, "submitted"); !errors.Is(err, ErrTurnConflict) {
					t.Fatal(err)
				}
				if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, "pending", result); !errors.Is(err, ErrTurnConflict) {
					t.Fatal(err)
				}
			}
			transition(t, s, tenant, session.ID, turn, TurnInProgress, terminal)
			assertNoPendingFunctions(t, s, tenant, session.ID, turn)
			if err := s.ConfirmFunctionResult(t.Context(), tenant, session.ID, turn, "submitted"); !errors.Is(err, ErrTurnConflict) {
				t.Fatal(err)
			}
			if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, "pending", result); !errors.Is(err, ErrTurnConflict) {
				t.Fatal(err)
			}
			if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, functionCallFixture("late")); !errors.Is(err, ErrTurnConflict) {
				t.Fatal(err)
			}
			if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, "submitted", result); err != nil {
				t.Fatal("identical retry changed its result", err)
			}
			row, err := s.GetFunctionCall(t.Context(), tenant, session.ID, turn, "submitted")
			if err != nil || row.Applied || len(row.Result) == 0 {
				t.Fatal("historical result lost or falsely applied", row, err)
			}
			next := submitMessage(t, s, tenant, session.ID, "next").TurnID
			transition(t, s, tenant, session.ID, next, TurnQueued, TurnInProgress)
			if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, next, functionCallFixture("pending")); err != nil {
				t.Fatal("call identity leaked across Turns", err)
			}
		})
	}
}

func assertNoPendingFunctions(t *testing.T, s *Store, tenant, session, turn string) {
	t.Helper()
	rows, err := s.PendingFunctionCalls(t.Context(), tenant, session, turn)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestFunctionResultConcurrentSubmissionsChooseOneValue(t *testing.T) {
	s, _ := testStore(t)
	other, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, functionCallFixture("call")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, output := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, _ := json.Marshal(map[string]any{"success": true, "output": output})
			outcomes <- other.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, "call", result)
		}()
	}
	wg.Wait()
	close(outcomes)
	winners, conflicts := 0, 0
	for err := range outcomes {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrIdempotencyConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal(winners, conflicts)
	}
}

func TestFunctionResultInvalidStorageInputDoesNotConsumeCall(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, functionCallFixture("call")); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "null", "[]", "{} {}", `{"output":"` + strings.Repeat("a", 512*1024) + `"}`} {
		if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, "call", json.RawMessage(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.SubmitFunctionResult(ctx, tenant, session.ID, turn, "call", json.RawMessage(`{"success":true}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	row, err := s.GetFunctionCall(t.Context(), tenant, session.ID, turn, "call")
	if err != nil || row.Result != nil || row.Applied {
		t.Fatal(row, err)
	}
}
