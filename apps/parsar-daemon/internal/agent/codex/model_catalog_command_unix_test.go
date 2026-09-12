//go:build unix

package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModelCatalogCancellationStopsDescendants(t *testing.T) {
	for _, finish := range []string{"wait", "exit 0"} {
		t.Run(finish, func(t *testing.T) {
			checkCatalogDescendantCancellation(t, finish)
		})
	}
}

func checkCatalogDescendantCancellation(t *testing.T, finish string) {
	t.Helper()
	started := time.Now()
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n(sleep 2; printf leaked > leaked) &\nprintf ready > ready\n"+finish+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- prepareModelVerbosity(ctx, binary, &SessionPlan{Cwd: dir}) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog launcher did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled catalog probe succeeded")
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("catalog probe kept waiting on child output pipes")
	}
	time.Sleep(time.Until(started.Add(2200 * time.Millisecond)))
	if _, err := os.Stat(filepath.Join(dir, "leaked")); !os.IsNotExist(err) {
		t.Fatalf("catalog child survived cancellation: %v", err)
	}
}
