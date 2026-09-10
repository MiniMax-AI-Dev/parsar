package installroot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockCaseAliases(t *testing.T) {
	parent := t.TempDir()
	alias := strings.ToUpper(parent)
	realInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, err := os.Stat(alias)
	if err != nil || !os.SameFile(realInfo, aliasInfo) {
		t.Skip("filesystem does not expose this case alias")
	}
	root := filepath.Join(parent, "new-root")
	unlock, err := Lock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	release, err := Lock(ctx, filepath.Join(alias, "NEW-ROOT"))
	if err == nil {
		release()
		t.Fatal("case alias acquired an independent lock")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("lock error = %v", err)
	}
}
