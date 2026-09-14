package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
)

func TestProjectScopesPersistAndRejectRemapping(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	ids := []string{uuid.NewString(), uuid.NewString()}
	sort.Strings(ids)
	original := identity.ProjectScope{TenantID: ids[1], OrganizationID: uuid.NewString(), ProjectID: uuid.NewString()}
	if err := s.EnsureProjectScopes(ctx, []identity.ProjectScope{original, original}); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	s, pool = testStore(t)
	if err := s.EnsureProjectScopes(ctx, []identity.ProjectScope{original}); err != nil {
		t.Fatalf("restart verification: %v", err)
	}
	newScope := identity.ProjectScope{TenantID: ids[0], OrganizationID: uuid.NewString(), ProjectID: uuid.NewString()}
	remapped := original
	remapped.ProjectID = uuid.NewString()
	if err := s.EnsureProjectScopes(ctx, []identity.ProjectScope{newScope, remapped}); !errors.Is(err, ErrProjectScopeConflict) {
		t.Fatalf("tenant remap = %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM execution_project_scopes WHERE tenant_id=$1", newScope.TenantID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial startup configuration = %d, %v", count, err)
	}
	remapped = original
	remapped.TenantID = uuid.NewString()
	if err := s.EnsureProjectScopes(ctx, []identity.ProjectScope{remapped}); !errors.Is(err, ErrProjectScopeConflict) {
		t.Fatalf("project remap = %v", err)
	}
	if err := s.EnsureProjectScopes(ctx, []identity.ProjectScope{newScope}); err != nil {
		t.Fatal(err)
	}
	var organization, project string
	if err := pool.QueryRow(ctx, "SELECT organization_id, project_id FROM execution_project_scopes WHERE tenant_id=$1", original.TenantID).Scan(&organization, &project); err != nil || organization != original.OrganizationID || project != original.ProjectID {
		t.Fatalf("removed key changed existing mapping: %q %q, %v", organization, project, err)
	}
}

func TestConcurrentProjectScopeBindingHasOneWinner(t *testing.T) {
	s, _ := testStore(t)
	a := identity.ProjectScope{TenantID: uuid.NewString(), OrganizationID: uuid.NewString(), ProjectID: uuid.NewString()}
	b := a
	b.ProjectID = uuid.NewString()
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for _, scope := range []identity.ProjectScope{a, b} {
		go func(scope identity.ProjectScope) {
			ready.Done()
			<-start
			results <- s.EnsureProjectScopes(t.Context(), []identity.ProjectScope{scope})
		}(scope)
	}
	ready.Wait()
	close(start)
	succeeded, conflicted := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrProjectScopeConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("successful/conflicting bindings = %d/%d", succeeded, conflicted)
	}
}
