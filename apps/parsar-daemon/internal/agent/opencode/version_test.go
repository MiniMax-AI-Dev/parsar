package opencode_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
)

func TestCheckCLIAvailableMissingBinaryWrapsErrCLINotFound(t *testing.T) {
	_, err := opencode.CheckCLIAvailable(context.Background(), "parsar-daemon-nonexistent-opencode-stub")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, opencode.ErrCLINotFound) {
		t.Fatalf("error %v does not wrap ErrCLINotFound", err)
	}
}

func TestCheckCLIAvailableDefaultsToOpenCodeBinary(t *testing.T) {
	_, err := opencode.CheckCLIAvailable(context.Background(), "")
	if err == nil {
		return
	}
	if !errors.Is(err, opencode.ErrCLINotFound) && !strings.Contains(err.Error(), "opencode") {
		t.Fatalf("error %q does not mention default binary name", err.Error())
	}
}

func TestCheckCLIAvailableReturnsTrimmedFirstLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only stub script; daemon doesn't target Windows")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "fake-opencode")
	body := "#!/bin/sh\nprintf 'opencode 9.9.9\\nextra line\\n'\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	ver, err := opencode.CheckCLIAvailable(context.Background(), stub)
	if err != nil {
		t.Fatalf("CheckCLIAvailable: %v", err)
	}
	if ver != "opencode 9.9.9" {
		t.Fatalf("version = %q, want opencode 9.9.9", ver)
	}
}

func TestCheckCLIAvailableSurfacesNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only stub script; daemon doesn't target Windows")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "broken-opencode")
	body := "#!/bin/sh\necho 'opencode kaboom' 1>&2\nexit 17\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	_, err := opencode.CheckCLIAvailable(context.Background(), stub)
	if err == nil {
		t.Fatal("expected non-nil error for broken stub")
	}
	if errors.Is(err, opencode.ErrCLINotFound) {
		t.Fatalf("broken-but-present binary should not wrap ErrCLINotFound: %v", err)
	}
	if !strings.Contains(err.Error(), "opencode kaboom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}
