package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestSessionMetadataPreservesCreationAndExecutionData(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Engine: "codex", IdempotencyKey: "metadata-update", Metadata: map[string]string{"old": "value"}, Configuration: []byte(`{"agent":{"model":"test-model"},"environment":{"type":"none"}}`)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var row string
		if err := pool.QueryRow(ctx, "SELECT (to_jsonb(s) - 'metadata')::text FROM sessions s WHERE tenant_id=$1 AND id=$2", tenant, first.ID).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	before := snapshot()
	for _, metadata := range []map[string]string{{"new": "value"}, nil, {}, {"unicode": "中文🧪"}} {
		updated, err := s.UpdateSessionMetadata(ctx, tenant, first.ID, metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		if err != nil || !reflect.DeepEqual(updated.Metadata, metadata) {
			t.Fatalf("update = %+v, %v", updated, err)
		}
		if snapshot() != before {
			t.Fatal("metadata update changed other stored Session fields")
		}
		retry, err := s.CreateSession(ctx, tenant, input)
		if err != nil || !reflect.DeepEqual(retry, updated) {
			t.Fatalf("creation retry = %+v, %v", retry, err)
		}
		changed := input
		changed.Metadata = metadata
		if _, err := s.CreateSession(ctx, tenant, changed); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("changed creation request: %v", err)
		}
	}
	current, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		tenant, session string
		metadata        map[string]string
		want            error
	}{
		{uuid.NewString(), first.ID, nil, ErrNotFound},
		{tenant, uuid.NewString(), nil, ErrNotFound},
		{"invalid", first.ID, nil, ErrInvalidInput},
		{tenant, "invalid", nil, ErrInvalidInput},
		{tenant, first.ID, map[string]string{"large": strings.Repeat("x", 64*1024)}, ErrInvalidInput},
	} {
		if _, err := s.UpdateSessionMetadata(ctx, test.tenant, test.session, test.metadata); !errors.Is(err, test.want) {
			t.Fatalf("rejected update error = %v, want %v", err, test.want)
		}
	}
	got, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil || !reflect.DeepEqual(got, current) {
		t.Fatalf("rejected update changed Session: %+v, %v", got, err)
	}
}

func TestSessionMetadataConcurrentReplacement(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	first, err := s.CreateSession(ctx, tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "concurrent-metadata"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprint(i)
			got, err := s.UpdateSessionMetadata(ctx, tenant, first.ID, map[string]string{key: key})
			if err != nil || len(got.Metadata) != 1 || got.Metadata[key] != key {
				t.Errorf("concurrent update = %+v, %v", got, err)
			}
		}()
	}
	wg.Wait()
	got, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil || len(got.Metadata) != 1 {
		t.Fatalf("concurrent replacements merged or lost metadata: %+v, %v", got, err)
	}
}

func TestSessionMetadataPreservesTerminalActivity(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "terminal-metadata"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{TurnCompleted, TurnFailed, TurnCancelled} {
		receipt, err := s.SubmitMessage(ctx, tenant, session.ID, uuid.NewString(), []byte(`{"text":"metadata fixture"}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.TransitionTurn(ctx, tenant, session.ID, receipt.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TransitionTurn(ctx, tenant, session.ID, receipt.TurnID, TurnTransition{ExpectedStatus: TurnInProgress, Status: status}); err != nil {
			t.Fatal(err)
		}
		before, err := s.GetSession(ctx, tenant, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		before.Metadata = map[string]string{"label": status}
		updated, err := s.UpdateSessionMetadata(ctx, tenant, session.ID, before.Metadata)
		if err != nil || !reflect.DeepEqual(updated, before) {
			t.Fatalf("metadata changed %s activity: %+v, %v", status, updated, err)
		}
	}
}
