package store

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func NewTestStore(t *testing.T) (*Store, *pgxpool.Pool) { return testStore(t) }
