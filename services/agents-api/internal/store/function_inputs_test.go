package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func resultInput(t *testing.T, turn, call, result string) Input {
	t.Helper()
	raw, err := json.Marshal(FunctionResultInput{TurnID: turn, CallID: call, Result: json.RawMessage(result)})
	if err != nil {
		t.Fatal(err)
	}
	return Input{Kind: "tool_result", Payload: raw}
}

func functionInputFixture(t *testing.T, s *Store) (string, Session, string) {
	t.Helper()
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	for _, id := range []string{"a", "b"} {
		if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, functionCallFixture(id)); err != nil {
			t.Fatal(err)
		}
	}
	return tenant, session, turn
}

func TestFunctionInputBatchesPersistAndReplayWithoutRetargeting(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, turn := functionInputFixture(t, s)
	full := `{"success":false,"output":[{"type":"input_text","text":""},{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":"after"}],"error":"failed"}`
	batch := []Input{resultInput(t, turn, "a", full), {Kind: "message", Payload: json.RawMessage(`{"text":"Follow up"}`)}, resultInput(t, turn, "b", `{"success":true,"output":null,"error":null}`), {Kind: "cancel", Payload: json.RawMessage(`{}`)}}
	receipts, err := s.SubmitInputs(t.Context(), tenant, session.ID, "batch", batch)
	if err != nil || len(receipts) != 4 {
		t.Fatal(receipts, err)
	}
	for i, receipt := range receipts {
		if receipt.Replayed || receipt.TurnID != turn || (i > 0 && receipt.Sequence <= receipts[i-1].Sequence) {
			t.Fatal(receipts)
		}
	}
	call, err := s.GetFunctionCall(t.Context(), tenant, session.ID, turn, "a")
	got, _ := canonicalJSONObject(call.Result)
	want, _ := canonicalJSONObject(json.RawMessage(full))
	if err != nil || call.Applied || string(got) != string(want) {
		t.Fatal(call, err)
	}
	// A result retry and its messages stay attached to their first Turn after restart.
	transition(t, s, tenant, session.ID, turn, TurnWaiting, TurnFailed)
	next := submitMessage(t, s, tenant, session.ID, "next").TurnID
	pool.Close()
	s, _ = testStore(t)
	retry, err := s.SubmitInputs(t.Context(), tenant, session.ID, "batch", batch)
	if err != nil || len(retry) != len(receipts) {
		t.Fatal(retry, err)
	}
	for i := range retry {
		if !retry[i].Replayed {
			t.Fatal(retry)
		}
		retry[i].Replayed = false
	}
	if !reflect.DeepEqual(retry, receipts) {
		t.Fatal(retry, receipts)
	}
	history, err := s.ListTurnInputs(t.Context(), tenant, session.ID, turn, 0, 100)
	if err != nil || len(history) != 5 || history[1].Kind != "tool_result" || history[2].Kind != "message" {
		t.Fatal(history, err)
	}
	future, err := s.ListTurnInputs(t.Context(), tenant, session.ID, next, 0, 100)
	if err != nil || len(future) != 1 {
		t.Fatal(future, err)
	}
	current, err := s.GetTurn(t.Context(), tenant, session.ID, next)
	if err != nil || !current.CancelRequestedAt.IsZero() || current.Status != TurnQueued {
		t.Fatal(current, err)
	}
	changed := []Input{batch[1], batch[0], batch[2], batch[3]}
	if _, err := s.SubmitInputs(t.Context(), tenant, session.ID, "batch", changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	// A new request identity can repeat an identical saved result, without native application.
	if _, err := s.SubmitInputs(t.Context(), tenant, session.ID, "same-result", batch[:1]); err != nil {
		t.Fatal(err)
	}
	call, err = s.GetFunctionCall(t.Context(), tenant, session.ID, turn, "a")
	if err != nil || call.Applied {
		t.Fatal(call, err)
	}
}

func TestFunctionInputBatchFailureRollsBackEveryWrite(t *testing.T) {
	for _, mode := range []string{"missing-call", "foreign-turn", "same-tenant-turn", "cancel-first", "changed-result"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := testStore(t)
			tenant, session, turn := functionInputFixture(t, s)
			message := Input{Kind: "message", Payload: json.RawMessage(`{"text":"Must roll back"}`)}
			cancel := Input{Kind: "cancel", Payload: json.RawMessage(`{}`)}
			first := resultInput(t, turn, "a", `{"success":true}`)
			batch := []Input{message, first, cancel}
			expected := ErrNotFound
			switch mode {
			case "missing-call":
				batch = append(batch, resultInput(t, turn, "missing", `{"success":true}`))
			case "foreign-turn", "same-tenant-turn":
				otherTenant := tenant
				if mode == "foreign-turn" {
					otherTenant = uuid.NewString()
				}
				other, err := s.CreateSession(t.Context(), otherTenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "other"})
				if err != nil {
					t.Fatal(err)
				}
				otherTurn := submitMessage(t, s, otherTenant, other.ID, "start").TurnID
				transition(t, s, otherTenant, other.ID, otherTurn, TurnQueued, TurnInProgress)
				if err := s.RecordFunctionCall(t.Context(), otherTenant, other.ID, otherTurn, functionCallFixture("a")); err != nil {
					t.Fatal(err)
				}
				batch = append(batch, resultInput(t, otherTurn, "a", `{"success":true}`))
			case "cancel-first":
				batch = []Input{message, cancel, first}
				expected = ErrTurnConflict
			case "changed-result":
				batch = append(batch, resultInput(t, turn, "a", `{"success":false}`))
				expected = ErrIdempotencyConflict
			}
			if _, err := s.SubmitInputs(t.Context(), tenant, session.ID, "failed-batch", batch); !errors.Is(err, expected) {
				t.Fatal(err)
			}
			call, err := s.GetFunctionCall(t.Context(), tenant, session.ID, turn, "a")
			if err != nil || call.Result != nil || call.Applied {
				t.Fatal(call, err)
			}
			state, err := s.GetTurn(t.Context(), tenant, session.ID, turn)
			if err != nil || !state.CancelRequestedAt.IsZero() || state.Status != TurnWaiting {
				t.Fatal(state, err)
			}
			history, err := s.ListTurnInputs(t.Context(), tenant, session.ID, turn, 0, 100)
			if err != nil || len(history) != 1 {
				t.Fatal(history, err)
			}
			if _, err := s.SubmitInputs(t.Context(), tenant, session.ID, "failed-batch", []Input{first, cancel}); err != nil {
				t.Fatal("failed transaction retained retry identity", err)
			}
		})
	}
}

func TestFunctionInputConcurrentBatchesSelectOneResult(t *testing.T) {
	s, _ := testStore(t)
	other, _ := testStore(t)
	tenant, session, turn := functionInputFixture(t, s)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		batch := []Input{{Kind: "message", Payload: json.RawMessage(fmt.Sprintf(`{"text":"message-%d"}`, i))}, resultInput(t, turn, "a", fmt.Sprintf(`{"success":true,"output":"%d"}`, i))}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := other.SubmitInputs(t.Context(), tenant, session.ID, fmt.Sprint(i), batch)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrIdempotencyConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	history, err := s.ListTurnInputs(t.Context(), tenant, session.ID, turn, 0, 100)
	if err != nil || len(history) != 3 {
		t.Fatal(history, err)
	}
}

func TestFunctionInputsRejectInvalidTargetsAndStorageObjects(t *testing.T) {
	s, _ := testStore(t)
	tenant, session, turn := functionInputFixture(t, s)
	for _, raw := range []string{`{}`, `{"turn_id":"bad","call_id":"a","result":{}}`, fmt.Sprintf(`{"turn_id":%q,"call_id":"a","result":null}`, turn), fmt.Sprintf(`{"turn_id":%q,"call_id":"a","result":[]}`, turn)} {
		_, err := s.SubmitInputs(t.Context(), tenant, session.ID, "invalid", []Input{{Kind: "tool_result", Payload: json.RawMessage(raw)}})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	input := resultInput(t, turn, "a", `{"success":true}`)
	for _, scope := range []struct{ tenant, session string }{{uuid.NewString(), session.ID}, {tenant, uuid.NewString()}} {
		if _, err := s.SubmitInputs(t.Context(), scope.tenant, scope.session, "foreign", []Input{input}); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if _, err := s.SubmitInputs(t.Context(), tenant, session.ID, "missing-turn", []Input{resultInput(t, uuid.NewString(), "a", `{"success":true}`)}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	transition(t, s, tenant, session.ID, turn, TurnWaiting, TurnFailed)
	if _, err := s.SubmitInputs(t.Context(), tenant, session.ID, "late", []Input{input}); !errors.Is(err, ErrTurnConflict) {
		t.Fatal(err)
	}
	current, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || current.LastTurn.ID != turn || current.LastTurn.Status != TurnFailed {
		t.Fatal(current, err)
	}
}
