package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func environmentInputSession(t *testing.T, s *Store) (string, Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(context.Background(), tenant, environmentInput("session", "self_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session
}

func reserveEnvironmentInput(t *testing.T, s *Store, tenant, session, key string) EnvironmentInputReservation {
	t.Helper()
	got, err := s.ReserveEnvironmentInput(context.Background(), tenant, session, key, []Input{messageInput("first"), messageInput("second")})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func environmentInputHistory(t *testing.T, pool *pgxpool.Pool, session string, turns, inputs int) {
	t.Helper()
	var gotTurns, gotInputs, items, events int
	err := pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM turns WHERE session_id=$1),
		       (SELECT count(*) FROM turn_inputs WHERE session_id=$1),
		       (SELECT count(*) FROM session_items WHERE session_id=$1),
		       (SELECT count(*) FROM session_events WHERE session_id=$1)`, session).Scan(&gotTurns, &gotInputs, &items, &events)
	if err != nil || gotTurns != turns || gotInputs != inputs || items != inputs || (inputs == 0 && events != 0) {
		t.Fatal("history", gotTurns, gotInputs, items, events, err)
	}
}

func TestEnvironmentInputReservationConcurrentIdentity(t *testing.T) {
	s, pool := testStore(t)
	other, _ := testStore(t)
	tenant, session := environmentInputSession(t, s)
	ctx := context.Background()
	batch := []Input{
		{Kind: "message", Payload: json.RawMessage(`{"text":"first","detail":{"a":1,"b":2}}`)},
		messageInput("second"),
	}
	const count = 8
	results := make(chan EnvironmentInputReservation, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := s
			inputs := append([]Input(nil), batch...)
			if i%2 == 0 {
				st = other
				inputs[0].Payload = json.RawMessage(` { "detail": {"b": 2, "a": 1}, "text": "first" } `)
			}
			got, err := st.ReserveEnvironmentInput(ctx, tenant, session.ID, "request", inputs)
			if err != nil {
				t.Error(err)
				return
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)
	var first EnvironmentInputReservation
	received := 0
	for result := range results {
		received++
		if first.ID == "" {
			first = result
		}
		if !reflect.DeepEqual(first, result) {
			t.Fatal("reservation identity changed", first, result)
		}
	}
	if received != count || first.State != EnvironmentInputPending || first.ID == "" || first.Deadline.Sub(first.CreatedAt) != 5*time.Minute || first.SettledAt != nil || len(first.Receipts) != 0 {
		t.Fatal("invalid pending result", received, first)
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
	for _, changed := range [][]Input{batch[:1], {batch[1], batch[0]}, {messageInput("changed"), batch[1]}} {
		if _, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "request", changed); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal("changed request accepted", err)
		}
	}
	if _, err := other.ReserveEnvironmentInput(ctx, tenant, session.ID, "other", batch); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("second pending request accepted", err)
	}
	pool.Close()
	restarted, _ := testStore(t)
	got, err := restarted.ReserveEnvironmentInput(ctx, tenant, session.ID, "request", batch)
	if err != nil || !reflect.DeepEqual(first, got) {
		t.Fatal("restart changed deadline or identity", got, err)
	}
}

func TestEnvironmentInputReservationPromotionAndDirectRetries(t *testing.T) {
	s, pool := testStore(t)
	lease := executionLease(t, s)
	writer := lease.Store()
	tenant, session := environmentInputSession(t, s)
	ctx := context.Background()
	first := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
	for _, request := range []struct {
		key    string
		inputs []Input
		want   error
	}{
		{"pending", first.Inputs, ErrTurnConflict},
		{"pending", []Input{messageInput("changed")}, ErrIdempotencyConflict},
		{"later", []Input{messageInput("later")}, ErrTurnConflict},
		{"cancel", []Input{{Kind: "cancel", Payload: json.RawMessage(`{}`)}}, ErrTurnConflict},
	} {
		if _, err := s.SubmitInputs(ctx, tenant, session.ID, request.key, request.inputs); !errors.Is(err, request.want) {
			t.Fatal("direct path bypassed reservation", request.key, err)
		}
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
	promoted, err := writer.PromoteEnvironmentInput(ctx, tenant, session.ID, first.ID)
	if err != nil || promoted.State != EnvironmentInputAdmitted || promoted.SettledAt == nil || len(promoted.Receipts) != 2 || !promoted.Deadline.Equal(first.Deadline) {
		t.Fatal(promoted, err)
	}
	for i, receipt := range promoted.Receipts {
		if receipt.Replayed || receipt.TurnID == "" || receipt.TurnID != promoted.Receipts[0].TurnID || (i > 0 && receipt.Sequence <= promoted.Receipts[i-1].Sequence) {
			t.Fatal("promotion receipts", promoted.Receipts)
		}
	}
	environmentInputHistory(t, pool, session.ID, 1, 2)
	for _, read := range []func() (EnvironmentInputReservation, error){
		func() (EnvironmentInputReservation, error) {
			return writer.PromoteEnvironmentInput(ctx, tenant, session.ID, first.ID)
		},
		func() (EnvironmentInputReservation, error) {
			return s.GetEnvironmentInputReservation(ctx, tenant, session.ID, first.ID)
		},
		func() (EnvironmentInputReservation, error) {
			return s.ReserveEnvironmentInput(ctx, tenant, session.ID, "pending", first.Inputs)
		},
	} {
		retry, err := read()
		if err != nil || retry.ID != first.ID || !retry.Deadline.Equal(first.Deadline) || retry.State != EnvironmentInputAdmitted || len(retry.Receipts) != 2 {
			t.Fatal(retry, err)
		}
		for i, receipt := range retry.Receipts {
			if !receipt.Replayed || receipt.Sequence != promoted.Receipts[i].Sequence || receipt.TurnID != promoted.Receipts[i].TurnID {
				t.Fatal("retry changed admission", receipt)
			}
		}
	}
	retry, err := s.SubmitInputs(ctx, tenant, session.ID, "pending", first.Inputs)
	if err != nil || len(retry) != 2 || !retry[0].Replayed || retry[0].Sequence != promoted.Receipts[0].Sequence {
		t.Fatal("direct retry after promotion", retry, err)
	}
	if err := lease.Close(ctx); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	restarted, pool := testStore(t)
	after, err := executionLease(t, restarted).Store().PromoteEnvironmentInput(ctx, tenant, session.ID, first.ID)
	if err != nil || after.State != EnvironmentInputAdmitted || after.Receipts[0].Sequence != promoted.Receipts[0].Sequence {
		t.Fatal("restart repeated promotion", after, err)
	}
	environmentInputHistory(t, pool, session.ID, 1, 2)
}

func TestEnvironmentInputReservationKeepsEarlierDirectIdentity(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := environmentInputSession(t, s)
	ctx := context.Background()
	input := messageInput("already admitted")
	receipts, err := s.SubmitInputs(ctx, tenant, session.ID, "direct", []Input{input})
	if err != nil {
		t.Fatal(err)
	}
	transition(t, s, tenant, session.ID, receipts[0].TurnID, TurnQueued, TurnInProgress)
	transition(t, s, tenant, session.ID, receipts[0].TurnID, TurnInProgress, TurnCompleted)
	pending := reserveEnvironmentInput(t, s, tenant, session.ID, "new")
	got, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "direct", []Input{input})
	if err != nil || got.State != EnvironmentInputAdmitted || got.ID != "" || !got.Deadline.IsZero() || len(got.Receipts) != 1 || got.Receipts[0].Sequence != receipts[0].Sequence {
		t.Fatal("direct admission gained a reservation", got, err)
	}
	if _, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "direct", []Input{messageInput("changed")}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	retry, err := s.SubmitInputs(ctx, tenant, session.ID, "direct", []Input{input})
	if err != nil || len(retry) != 1 || !retry[0].Replayed {
		t.Fatal(retry, err)
	}
	retained, err := s.GetEnvironmentInputReservation(ctx, tenant, session.ID, pending.ID)
	if err != nil || !reflect.DeepEqual(retained, pending) {
		t.Fatal("old retry affected new pending input", retained, err)
	}
}

func TestEnvironmentInputReservationRejectsUnsupportedOrForeignState(t *testing.T) {
	s, _ := testStore(t)
	lease := executionLease(t, s)
	writer := lease.Store()
	tenant, session := environmentInputSession(t, s)
	ctx := context.Background()
	pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
	for _, read := range []func() error{
		func() error {
			_, err := s.GetEnvironmentInputReservation(ctx, uuid.NewString(), session.ID, pending.ID)
			return err
		},
		func() error {
			_, err := writer.PromoteEnvironmentInput(ctx, uuid.NewString(), session.ID, pending.ID)
			return err
		},
		func() error {
			_, err := s.CancelEnvironmentInput(ctx, uuid.NewString(), session.ID, pending.ID)
			return err
		},
		func() error {
			_, err := s.ReserveEnvironmentInput(ctx, uuid.NewString(), session.ID, "new", pending.Inputs)
			return err
		},
	} {
		if err := read(); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign access", err)
		}
	}
	other, err := s.CreateSession(ctx, tenant, environmentInput("other", "self_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []func(context.Context, string, string, string) (EnvironmentInputReservation, error){
		s.GetEnvironmentInputReservation, writer.PromoteEnvironmentInput, s.CancelEnvironmentInput, s.ExpireEnvironmentInput,
	} {
		if _, err := action(ctx, tenant, other.ID, pending.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("reservation crossed Session ownership", err)
		}
		if _, err := action(ctx, uuid.NewString(), session.ID, pending.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("reservation crossed tenant ownership", err)
		}
	}
	retained, err := s.GetEnvironmentInputReservation(ctx, tenant, session.ID, pending.ID)
	if err != nil || !reflect.DeepEqual(retained, pending) {
		t.Fatal("foreign operations changed reservation", retained, err)
	}
	for _, invalid := range [][]Input{nil, {{Kind: "cancel", Payload: json.RawMessage(`{}`)}}, {{Kind: "tool_result", Payload: json.RawMessage(`{}`)}}} {
		if _, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "invalid", invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("unsupported reservation", err)
		}
	}
	noneTenant, none := newTurnSession(t, s)
	if _, err := s.ReserveEnvironmentInput(ctx, noneTenant, none.ID, "none", pending.Inputs); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("none reservation", err)
	}
	activeTenant, active := environmentInputSession(t, s)
	submitMessage(t, s, activeTenant, active.ID, "active")
	if _, err := s.ReserveEnvironmentInput(ctx, activeTenant, active.ID, "new", pending.Inputs); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("active Turn reservation", err)
	}
}
