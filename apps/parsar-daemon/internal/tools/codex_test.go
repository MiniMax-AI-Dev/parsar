package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireDirLockHonorsContext(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "codex.lock")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := acquireDirLock(ctx, lockDir); err == nil {
		t.Fatal("expected context deadline while lock is held")
	}
}
