package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSavedAgentsPersistIndependentlyAndStayTenantScoped(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	session, err := s.CreateSession(ctx, tenantA, CreateSessionInput{Engine: "codex", IdempotencyKey: "session"})
	if err != nil {
		t.Fatal(err)
	}
	// Configuration is preserved without applying one harness's capabilities.
	input := CreateAgentInput{
		Metadata:      map[string]string{"purpose": "保存 configuration"},
		Configuration: []byte(`{"model":" caller-model ","name":null,"instructions":" keep whitespace ","multi_agent":{"enabled":true,"max_concurrent_subagents":6},"tools":[{"type":"function","name":"lookup","description":"","defer_loading":true,"parameters":{"type":"object","properties":{"number":{"const":9007199254740993}}}}]}`),
	}
	before := time.Now().Add(-time.Second)
	first, err := s.CreateAgent(ctx, tenantA, input)
	if err != nil {
		t.Fatal(err)
	}
	expectedConfig, err := canonicalJSONObject(input.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	gotConfig, err := canonicalJSONObject(first.Configuration)
	if err != nil || string(gotConfig) != string(expectedConfig) {
		t.Fatalf("configuration changed: %s, %v", first.Configuration, err)
	}
	if first.ID == session.ID || first.TenantID != tenantA || !reflect.DeepEqual(first.Metadata, input.Metadata) ||
		first.CreatedAt.Before(before) || first.CreatedAt.After(time.Now().Add(time.Second)) || !first.CreatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("unexpected saved agent: %+v", first)
	}
	// Identical configurations are distinct resources; storage invents no public
	// create-idempotency contract or shared identity across callers.
	for _, tenant := range []string{tenantA, tenantB} {
		other, err := s.CreateAgent(ctx, tenant, input)
		if err != nil || other.ID == first.ID || other.TenantID != tenant {
			t.Fatalf("distinct create: %+v, %v", other, err)
		}
	}
	for _, lookup := range []struct{ tenant, id string }{
		{tenantB, first.ID}, {tenantA, uuid.NewString()}, {tenantA, session.ID},
	} {
		if _, err := s.GetAgent(ctx, lookup.tenant, lookup.id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unowned/absent agent read: %v", err)
		}
	}
	if _, err := s.GetSession(ctx, tenantA, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("saved agent became an execution session: %v", err)
	}
	unchanged, err := s.GetSession(ctx, tenantA, session.ID)
	if err != nil || !reflect.DeepEqual(unchanged, session) {
		t.Fatalf("saved Agent storage changed Session: %+v, %v", unchanged, err)
	}
	pool.Close()
	recovered, _ := testStore(t)
	got, err := recovered.GetAgent(ctx, tenantA, first.ID)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("durable read: %+v, %v; want %+v", got, err, first)
	}
	emptyMetadata, err := recovered.CreateAgent(ctx, tenantA, CreateAgentInput{Configuration: []byte(`{"model":"another-model"}`)})
	if err != nil || emptyMetadata.Metadata == nil || len(emptyMetadata.Metadata) != 0 {
		t.Fatalf("empty metadata: %+v, %v", emptyMetadata, err)
	}
}

func TestSavedAgentsRejectInvalidStoreInput(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	for _, raw := range []string{"", "null", "[]", "true", `{"model":"x"`, `{} {}`, `{"x":"` + strings.Repeat("x", 512*1024) + `"}`} {
		if _, err := s.CreateAgent(ctx, tenant, CreateAgentInput{Configuration: []byte(raw)}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid configuration length %d: %v", len(raw), err)
		}
	}
	valid := CreateAgentInput{Configuration: []byte(`{"model":"x"}`)}
	for _, invalid := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		if _, err := s.CreateAgent(ctx, invalid, valid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid tenant accepted: %v", err)
		}
		if _, err := s.GetAgent(ctx, invalid, uuid.NewString()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid read tenant accepted: %v", err)
		}
		if _, err := s.GetAgent(ctx, tenant, invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid ID accepted: %v", err)
		}
	}
	valid.Metadata = map[string]string{"large": strings.Repeat("x", 64*1024)}
	if _, err := s.CreateAgent(ctx, tenant, valid); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized metadata accepted: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM agents WHERE tenant_id = $1", tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected input wrote %d rows: %v", count, err)
	}
}
