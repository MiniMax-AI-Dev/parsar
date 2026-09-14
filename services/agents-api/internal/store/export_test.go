package store

import (
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func NewTestStore(t *testing.T) (*Store, *pgxpool.Pool) { return testStore(t) }

// FixtureCreator is an explicit synthetic principal for newly created test Sessions.
func FixtureCreator() identity.Subject {
	return identity.Subject{Kind: "service_account", ID: "test-runner"}
}

// FixtureExecutorPrincipal explicitly provisions a synthetic project for executor fixtures.
func FixtureExecutorPrincipal(t *testing.T, s *Store, tenant string) identity.Principal {
	t.Helper()
	p := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: tenant, OrganizationID: "test-org", ProjectID: tenant}, SubjectKind: FixtureCreator().Kind, SubjectID: FixtureCreator().ID}
	if err := s.EnsureProjectScopes(t.Context(), []identity.ProjectScope{p.ProjectScope}); err != nil {
		t.Fatal(err)
	}
	return p
}
