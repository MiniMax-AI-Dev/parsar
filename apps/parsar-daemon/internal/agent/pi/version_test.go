package pi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
)

func TestCheckCLIAvailableMissingBinaryWrapsErrCLINotFound(t *testing.T) {
	_, err := pi.CheckCLIAvailable(context.Background(), "parsar-daemon-nonexistent-pi-stub")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, pi.ErrCLINotFound) {
		t.Fatalf("error %v does not wrap ErrCLINotFound", err)
	}
}

func TestCheckCLIAvailableDefaultsToPiBinary(t *testing.T) {
	_, err := pi.CheckCLIAvailable(context.Background(), "")
	if err == nil {
		return
	}
	if !errors.Is(err, pi.ErrCLINotFound) && !strings.Contains(err.Error(), "pi") {
		t.Fatalf("error %q does not mention default binary name", err.Error())
	}
}

func TestCheckCLIAvailableReturnsTrimmedFirstLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only stub script; daemon doesn't target Windows")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "fake-pi")
	body := "#!/bin/sh\nprintf 'pi 9.9.9\\nextra line\\n'\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	ver, err := pi.CheckCLIAvailable(context.Background(), stub)
	if err != nil {
		t.Fatalf("CheckCLIAvailable: %v", err)
	}
	if ver != "pi 9.9.9" {
		t.Fatalf("version = %q, want pi 9.9.9", ver)
	}
}

func TestCheckCLIAvailableSurfacesNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only stub script; daemon doesn't target Windows")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "broken-pi")
	body := "#!/bin/sh\necho 'pi kaboom' 1>&2\nexit 17\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	_, err := pi.CheckCLIAvailable(context.Background(), stub)
	if err == nil {
		t.Fatal("expected non-nil error for broken stub")
	}
	if errors.Is(err, pi.ErrCLINotFound) {
		t.Fatalf("broken-but-present binary should not wrap ErrCLINotFound: %v", err)
	}
	if !strings.Contains(err.Error(), "pi kaboom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}
