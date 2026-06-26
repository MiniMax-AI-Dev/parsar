package claudecode_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
)

func TestCheckCLIAvailableMissingBinaryWrapsErrCLINotFound(t *testing.T) {
	_, err := claudecode.CheckCLIAvailable(context.Background(), "parsar-daemon-nonexistent-claude-stub")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, claudecode.ErrCLINotFound) {
		t.Fatalf("error %v does not wrap ErrCLINotFound", err)
	}
}

func TestCheckCLIAvailableDefaultsToClaudeBinary(t *testing.T) {
	// Empty binary should normalise to "claude".
	_, err := claudecode.CheckCLIAvailable(context.Background(), "")
	if err == nil {
		return // claude installed locally; success covered by integration runs.
	}
	if !errors.Is(err, claudecode.ErrCLINotFound) {
		if msg := err.Error(); !contains(msg, "claude") {
			t.Fatalf("error %q does not mention default binary name", msg)
		}
	}
}

func TestCheckCLIAvailableReturnsTrimmedFirstLine(t *testing.T) {
	// posix-only stub script; daemon doesn't target Windows.
	if runtime.GOOS == "windows" {
		t.Skip("posix-only stub script; daemon doesn't target Windows")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "fake-claude")
	body := "#!/bin/sh\nprintf 'fake 9.9.9 (build abc)\\n  extra line\\n'\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	ver, err := claudecode.CheckCLIAvailable(context.Background(), stub)
	if err != nil {
		t.Fatalf("CheckCLIAvailable: %v", err)
	}
	want := "fake 9.9.9 (build abc)"
	if ver != want {
		t.Fatalf("version=%q want %q", ver, want)
	}
}

func TestCheckCLIAvailableSurfacesNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only stub script; daemon doesn't target Windows")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "broken-claude")
	body := "#!/bin/sh\necho 'kaboom' 1>&2\nexit 17\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	_, err := claudecode.CheckCLIAvailable(context.Background(), stub)
	if err == nil {
		t.Fatal("expected non-nil error for broken stub")
	}
	if errors.Is(err, claudecode.ErrCLINotFound) {
		t.Fatalf("broken-but-present binary should not wrap ErrCLINotFound: %v", err)
	}
	if !contains(err.Error(), "kaboom") {
		t.Fatalf("expected stderr %q in error %v", "kaboom", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
