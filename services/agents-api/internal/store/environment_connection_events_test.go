package store_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func awaitEnvironmentConnectionState(t *testing.T, ctx context.Context, s *store.Store, tenant, environment, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := s.GetEnvironment(ctx, tenant, environment)
		if err != nil {
			t.Fatal(err)
		}
		if value.Status == status {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("persisted connection state did not converge", status)
}

func retainedEnvironmentEvents(t *testing.T, ctx context.Context, s *store.Store, tenant, session, environment string) []v1.SessionEvent {
	t.Helper()
	var after int64
	events := []v1.SessionEvent{}
	for {
		changes, err := s.ListSessionEvents(ctx, tenant, session, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) == 0 {
			break
		}
		for _, change := range changes {
			after = change.Sequence
			if change.Event.Environment == nil {
				continue
			}
			event := change.Event
			if event.SessionID != session || event.Environment.ID != environment || event.Environment.Type != "self_hosted" || event.Environment.Error != nil || event.TurnID != "" {
				t.Fatal("invalid transport event identity")
			}
			status := event.Environment.Status
			if (status != "connected" && status != "disconnected") || event.Type != "agent.session.environment."+status {
				t.Fatal("transport observation claimed native readiness")
			}
			if len(events) > 0 && events[len(events)-1].Type == event.Type {
				t.Fatal("duplicate lifecycle transition")
			}
			events = append(events, event)
		}
	}
	return events
}

func verifyEnvironmentEventsWithSDK(t *testing.T, root string, events []v1.SessionEvent) {
	t.Helper()
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Fatal("pinned official SDK is required for native Environment event acceptance")
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "environment-events.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "../../tests/official_environment_events.py", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official Environment event validation: %v\n%s", err, output)
	}
}
