package main

import (
	"context"
	"testing"
	"time"
)

type fakeRuntimeHeartbeatStore struct{}

func (fakeRuntimeHeartbeatStore) SweepStaleRuntimes(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func TestBuildRuntimeHeartbeatSweeper(t *testing.T) {
	if sw := buildRuntimeHeartbeatSweeper(envMap(nil), nil); sw != nil {
		t.Fatal("nil store: expected nil sweeper")
	}
	if sw := buildRuntimeHeartbeatSweeper(envMap(map[string]string{
		"PARSAR_RUNTIME_HEARTBEAT_STALE_SECONDS": "0",
	}), fakeRuntimeHeartbeatStore{}); sw != nil {
		t.Fatal("stale seconds 0: expected nil sweeper")
	}
	if sw := buildRuntimeHeartbeatSweeper(envMap(map[string]string{
		"PARSAR_RUNTIME_HEARTBEAT_STALE_SECONDS": "nope",
	}), fakeRuntimeHeartbeatStore{}); sw != nil {
		t.Fatal("invalid stale seconds: expected nil sweeper")
	}
	if sw := buildRuntimeHeartbeatSweeper(envMap(nil), fakeRuntimeHeartbeatStore{}); sw == nil {
		t.Fatal("default env: expected sweeper")
	}
	if sw := buildRuntimeHeartbeatSweeper(envMap(map[string]string{
		"PARSAR_RUNTIME_HEARTBEAT_STALE_SECONDS":          "90",
		"PARSAR_RUNTIME_HEARTBEAT_SWEEP_INTERVAL_SECONDS": "bad",
	}), fakeRuntimeHeartbeatStore{}); sw == nil {
		t.Fatal("invalid interval should fall back, not disable")
	}
}
