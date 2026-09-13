package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSessionAgentFilterPaginationAndIsolation(t *testing.T) {
	s, pool := testStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	root := "agent_inline-root"
	var expected []string
	create := func(tenant, key, agent string) Session {
		configuration, _ := json.Marshal(map[string]any{"agent": map[string]string{"id": agent, "model": "test-model"}, "environment": map[string]string{"type": "none"}})
		value, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: key, Configuration: configuration})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for i := range 9 {
		agent := "other-agent"
		if i%2 == 0 {
			agent = root
		}
		value := create(tenant, fmt.Sprint(i), agent)
		if agent == root {
			expected = append(expected, value.ID)
		}
	}
	other := create(foreign, "foreign", root)
	if _, err := pool.Exec(t.Context(), "UPDATE sessions SET created_at=$1 WHERE tenant_id=$2", time.Unix(1700000000, 0), tenant); err != nil {
		t.Fatal(err)
	}
	slices.Sort(expected)
	read := func(s *Store, ascending bool) []string {
		var got []string
		cursor := ""
		for {
			page, err := s.ListSessions(t.Context(), tenant, cursor, 2, ascending, &root)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Sessions) == 0 || len(page.Sessions) > 2 {
				t.Fatal(page)
			}
			for _, value := range page.Sessions {
				if value.TenantID != tenant {
					t.Fatal("foreign Session", value.ID)
				}
				got = append(got, value.ID)
			}
			if page.NextCursor == "" {
				return got
			}
			if page.NextCursor != page.Sessions[len(page.Sessions)-1].ID || len(got) > len(expected) {
				t.Fatal("invalid continuation", page)
			}
			cursor = page.NextCursor
		}
	}
	if got := read(s, true); !slices.Equal(got, expected) {
		t.Fatal(got, expected)
	}
	reverse := slices.Clone(expected)
	slices.Reverse(reverse)
	if got := read(s, false); !slices.Equal(got, reverse) {
		t.Fatal(got, reverse)
	}
	unfiltered, err := s.ListSessions(t.Context(), tenant, "", 100, false, nil)
	if err != nil || len(unfiltered.Sessions) != 9 {
		t.Fatal(unfiltered, err)
	}
	for _, id := range []string{"", "unknown", root + " ", "' OR true --"} {
		page, err := s.ListSessions(t.Context(), tenant, "", 100, false, &id)
		if err != nil || page.Sessions == nil || len(page.Sessions) != 0 || page.NextCursor != "" {
			t.Fatal(page, err)
		}
	}
	if _, err := s.ListSessions(t.Context(), tenant, other.ID, 2, true, &root); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign cursor", err)
	}
	page, err := s.ListSessions(t.Context(), foreign, "", 100, false, &root)
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != other.ID {
		t.Fatal(page, err)
	}
	pool.Close()
	restored, _ := testStore(t)
	if got := read(restored, true); !slices.Equal(got, expected) {
		t.Fatal(got, expected)
	}
}
