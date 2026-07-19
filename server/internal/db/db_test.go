package db

import (
	"context"
	"testing"
	"time"
)

// TestOpenPoolRejectsUnreachableHost verifies invalid address must surface as an
// error rather than hanging. Tight deadline so the test fails fast on
// misconfigured DNS or slow networks.
func TestOpenPoolRejectsUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pool, err := OpenPool(ctx, "postgres://parsar:parsar@127.0.0.1:1/parsar?sslmode=disable&connect_timeout=1")
	if err == nil {
		pool.Close()
		t.Fatal("expected error connecting to unreachable host")
	}
}

func TestDefaultDatabaseConfig(t *testing.T) {
	if DefaultDatabaseURL == "" {
		t.Fatal("DefaultDatabaseURL must be set")
	}
	if DefaultDatabaseName == "" {
		t.Fatal("DefaultDatabaseName must be set")
	}
}
