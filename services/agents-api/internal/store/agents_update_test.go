package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentUpdateRollbackAndCompleteSizeBound(t *testing.T) {
	s, _ := testStore(t)
	ctx := t.Context()
	tenant := uuid.NewString()
	configuration, err := json.Marshal(map[string]any{"model": "original", "instructions": strings.Repeat("x", 400*1024), "number": json.Number("9007199254740993")})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.CreateAgent(ctx, tenant, CreateAgentInput{Configuration: configuration, Metadata: map[string]string{"keep": "original"}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"replace": "not-committed"}
	oversized, err := json.Marshal(map[string]string{"name": strings.Repeat("y", 150*1024)})
	if err != nil {
		t.Fatal(err)
	}
	for _, patch := range []json.RawMessage{oversized, []byte(`[]`), []byte(`{"name":"bad"} {}`)} {
		_, err := s.UpdateAgent(ctx, tenant, original.ID, UpdateAgentInput{Configuration: patch, Metadata: &metadata})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid update: %v", err)
		}
		unchanged, err := s.GetAgent(ctx, tenant, original.ID)
		if err != nil || !reflect.DeepEqual(unchanged, original) {
			t.Fatalf("partial failed update: %v", err)
		}
	}
	_, err = s.UpdateAgent(ctx, uuid.NewString(), original.ID, UpdateAgentInput{Configuration: []byte(`{"model":"foreign"}`), Metadata: &metadata})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign update: %v", err)
	}
	// A failure releases the lock; a later valid patch preserves unrelated large values and numbers.
	updated, err := s.UpdateAgent(ctx, tenant, original.ID, UpdateAgentInput{Configuration: []byte(`{"model":"updated"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(updated.Configuration, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["number"]) != "9007199254740993" || string(fields["model"]) != `"updated"` || !reflect.DeepEqual(updated.Metadata, original.Metadata) {
		t.Fatal("unrelated configuration or metadata lost")
	}
	if !updated.CreatedAt.Equal(original.CreatedAt) || updated.UpdatedAt.Before(original.UpdatedAt) {
		t.Fatal("resource timestamps changed incorrectly")
	}
	unchanged, err := s.UpdateAgent(ctx, tenant, original.ID, UpdateAgentInput{})
	if err != nil || !reflect.DeepEqual(unchanged, updated) {
		t.Fatalf("empty update: %v", err)
	}
}
