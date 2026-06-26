package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallScriptInSyncWithRepoCopy fails CI if the user-visible copy
// at scripts/install-parsar-daemon.sh drifts from the go:embed copy here.
// Two copies because the repo-root one is browsable + runnable standalone;
// the embed one is what the server serves at runtime.
func TestInstallScriptInSyncWithRepoCopy(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..", "..")
	repoCopy, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "install-parsar-daemon.sh"))
	if err != nil {
		t.Fatalf("read repo copy: %v", err)
	}
	embedCopy, err := os.ReadFile("install_parsar_daemon.sh")
	if err != nil {
		t.Fatalf("read embed copy: %v", err)
	}
	if string(repoCopy) != string(embedCopy) {
		t.Fatalf("scripts/install-parsar-daemon.sh and server/internal/api/install_parsar_daemon.sh drifted; copy one to the other and re-run the test")
	}
}
