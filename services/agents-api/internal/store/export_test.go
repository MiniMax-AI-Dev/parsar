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
