package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAgentListPaginationIsolationAndReconnect(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant, other := uuid.NewString(), uuid.NewString()
	empty, err := s.ListAgents(ctx, tenant, "", 2, false)
	if err != nil || empty.Agents == nil || len(empty.Agents) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty page: %+v, %v", empty, err)
	}
	input := CreateAgentInput{Configuration: []byte(`{"model":"unchanged","tools":[{"parameters":{"const":9007199254740993}}]}`), Metadata: map[string]string{"scope": "same-tenant"}}
	ids := []string{}
	for range 5 {
		agent, err := s.CreateAgent(ctx, tenant, input)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, agent.ID)
	}
	foreign, err := s.CreateAgent(ctx, other, input)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0).UTC()
	if _, err := pool.Exec(ctx, "UPDATE agents SET created_at=$1, updated_at=$1 WHERE tenant_id=$2", stamp, tenant); err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	read := func(s *Store, ascending bool) []string {
		t.Helper()
		var actual []string
		cursor := ""
		for {
			page, err := s.ListAgents(ctx, tenant, cursor, 2, ascending)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Agents) == 0 || len(page.Agents) > 2 {
				t.Fatalf("bad page: %+v", page)
			}
			for _, agent := range page.Agents {
				original, err := s.GetAgent(ctx, tenant, agent.ID)
				if err != nil || !reflect.DeepEqual(agent, original) || agent.TenantID != tenant {
					t.Fatalf("resource changed: %+v, %v", agent, err)
				}
				actual = append(actual, agent.ID)
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Agents[len(page.Agents)-1].ID || len(actual) > len(ids) {
				t.Fatal("invalid/repeating continuation")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	if got := read(s, true); !slices.Equal(got, ids) {
		t.Fatalf("ascending equal timestamps: %v", got)
	}
	reverse := slices.Clone(ids)
	slices.Reverse(reverse)
	if got := read(s, false); !slices.Equal(got, reverse) {
		t.Fatalf("descending equal timestamps: %v", got)
	}
	for _, after := range []string{foreign.ID, uuid.NewString()} {
		if _, err := s.ListAgents(ctx, tenant, after, 2, true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unowned/unknown cursor accepted: %v", err)
		}
	}
	if _, err := s.ListAgents(ctx, tenant, "not-an-id", 2, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid cursor accepted: %v", err)
	}
	tail, err := s.ListAgents(ctx, tenant, ids[len(ids)-1], 2, true)
	if err != nil || tail.Agents == nil || len(tail.Agents) != 0 || tail.NextCursor != "" {
		t.Fatalf("end page: %+v, %v", tail, err)
	}
	foreignPage, err := s.ListAgents(ctx, other, "", 100, false)
	if err != nil || len(foreignPage.Agents) != 1 || foreignPage.Agents[0].ID != foreign.ID {
		t.Fatalf("tenant isolation: %+v, %v", foreignPage, err)
	}
	pool.Close()
	restored, _ := testStore(t)
	if got := read(restored, true); !slices.Equal(got, ids) {
		t.Fatalf("pagination changed after reconnect: %v", got)
	}
}
