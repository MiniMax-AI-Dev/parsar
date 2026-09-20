package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
)

type interruptedStore struct {
	Store
	operation string
	fired     bool
}

func (s *interruptedStore) interrupt(operation string) error {
	if !s.fired && s.operation == operation {
		s.fired = true
		return &pgconn.PgError{Code: "57P01", Message: "database restarting"}
	}
	return nil
}
func (s *interruptedStore) GetCoreSession(ctx context.Context, id string) (store.CoreSessionBinding, error) {
	if err := s.interrupt("read_session"); err != nil {
		return store.CoreSessionBinding{}, err
	}
	return s.Store.GetCoreSession(ctx, id)
}
func (s *interruptedStore) EnsureCoreRun(ctx context.Context, id, binding string, input json.RawMessage) error {
	if err := s.interrupt("ensure_run"); err != nil {
		return err
	}
	return s.Store.EnsureCoreRun(ctx, id, binding, input)
}
func (s *interruptedStore) BindCoreSession(ctx context.Context, id, session string) error {
	if err := s.interrupt("bind_session"); err != nil {
		return err
	}
	return s.Store.BindCoreSession(ctx, id, session)
}
func (s *interruptedStore) MarkCoreRunSubmitted(ctx context.Context, id string) error {
	if err := s.interrupt("submission_receipt"); err != nil {
		return err
	}
	return s.Store.MarkCoreRunSubmitted(ctx, id)
}
func (s *interruptedStore) BindCoreTurn(ctx context.Context, id, turn string) error {
	if err := s.interrupt("bind_turn"); err != nil {
		return err
	}
	return s.Store.BindCoreTurn(ctx, id, turn)
}
func (s *interruptedStore) ListCoreExecutionEvents(ctx context.Context, id string, after int64) ([]store.AgentRunEventRead, error) {
	if err := s.interrupt("restore_projection"); err != nil {
		return nil, err
	}
	return s.Store.ListCoreExecutionEvents(ctx, id, after)
}
func (s *interruptedStore) RecordCoreUsage(ctx context.Context, id string, usage store.UsageInput) error {
	if err := s.interrupt("usage"); err != nil {
		return err
	}
	return s.Store.RecordCoreUsage(ctx, id, usage)
}
func (s *interruptedStore) SettleCoreRun(ctx context.Context, id string) error {
	if err := s.interrupt("settlement"); err != nil {
		return err
	}
	return s.Store.SettleCoreRun(ctx, id)
}

func TestPersistenceInterruptionPreservesCoreExecution(t *testing.T) {
	for _, operation := range []string{"read_session", "bind_session", "ensure_run", "submission_receipt", "bind_turn", "restore_projection", "usage", "settlement"} {
		t.Run(operation, func(t *testing.T) {
			st := &memoryStore{status: "running"}
			api := &protocolServer{status: "completed"}
			c := newTestConnector(t, st, api)
			fault := &interruptedStore{Store: st, operation: operation}
			c.store = fault
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if operation == "read_session" {
				_, err := c.StreamPrompt(ctx, testInput())
				if !errors.Is(err, connector.ErrObservationInterrupted) {
					t.Fatalf("read became execution failure: %v", err)
				}
			} else {
				for _, event := range drain(t, ctx, c, st) {
					if event.Type == connector.EventError || (event.Type == connector.EventDone && operation != "settlement") {
						t.Fatalf("persistence failure became terminal: %+v", event)
					}
				}
			}
			if !fault.fired {
				t.Fatal("fault not exercised")
			}
			if st.run.Settled || api.cancels != 0 {
				t.Fatalf("interrupted execution was settled or cancelled: %+v", st.run)
			}
			events := drain(t, ctx, c, st)
			if len(events) == 0 || events[len(events)-1].Type != connector.EventDone || api.turns != 1 || api.cancels != 0 || !st.run.Settled {
				t.Fatalf("recovery lost execution: events=%+v turns=%d cancels=%d run=%+v", events, api.turns, api.cancels, st.run)
			}
		})
	}
}
