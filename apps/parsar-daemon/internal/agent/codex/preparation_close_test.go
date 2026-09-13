package codex

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPreparedCloseWaitsForOwnerCleanup(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	owner, cancel := context.WithCancel(t.Context())
	defer cancel()
	p, err := newPreparation(owner, req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	cleanup := p.plan.Cleanup
	p.plan.Cleanup = sync.OnceFunc(func() { close(entered); <-release; cleanup() })
	cancel()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("owner watcher did not enter cleanup")
	}
	done := make(chan struct{})
	go func() { _ = p.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("Close returned while owner cleanup was still running")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not finish after cleanup")
	}
	waitPreparedRelease(t, p, root)
}
